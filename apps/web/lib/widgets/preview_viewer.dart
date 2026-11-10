import 'dart:async';
import 'dart:ui' show SemanticsHitTestBehavior;

import 'package:flutter/services.dart';
import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../viewer/viewer_adapter.dart';
import '../viewer/playback_controller.dart';
import '../viewer/viewer_selection.dart';
import 'edge_seek_bar.dart';

class PreviewViewer extends StatefulWidget {
  const PreviewViewer({
    required this.source,
    required this.factory,
    this.freeView = false,
    this.thumbnailSource,
    this.visible = true,
    this.onReady,
    this.onStatusChanged,
    this.onSourceReady,
    this.onSourceError,
    this.sha256,
    this.sourceRevision = 0,
    this.controller,
    this.showControls = true,
    this.interactive = true,
    this.playback,
    this.continuity = false,
    this.mapping,
    this.transportIdentity,
    this.onValueChanged,
    this.onPrepared,
    this.onPlaybackRestored,
    this.onPlaybackInteraction,
    super.key,
  });

  final Uri source;
  final ViewerFactory factory;
  final bool freeView;
  final Uri? thumbnailSource;

  /// Retains layout while pausing rendering and excluding hidden controls.
  /// The host paints its visible content above this surface when false.
  final bool visible;
  final VoidCallback? onReady;
  final ValueChanged<ViewerStatus>? onStatusChanged;
  final ValueChanged<Uri>? onSourceReady;
  final VoidCallback? onSourceError;
  final String? sha256;
  final int sourceRevision;
  final PlaybackController? controller;
  final bool showControls, interactive;
  final PlaybackState? playback;
  final bool continuity;
  final PreviewSceneMapping? mapping;
  final Map<String, String>? transportIdentity;
  final ValueChanged<ViewerValue>? onValueChanged;
  final ValueChanged<ViewerTimeline>? onPrepared;
  final VoidCallback? onPlaybackRestored;
  final ValueChanged<ViewerValue>? onPlaybackInteraction;

  @override
  State<PreviewViewer> createState() => _PreviewViewerState();
}

class _PreviewViewerState extends State<PreviewViewer>
    with WidgetsBindingObserver {
  late ViewerAdapter _adapter;
  late ViewerSelection<ViewerValue> _updates;
  late ViewerSelection<Object> _presentation;
  final _playbackFocus = FocusNode();
  bool _hasReadyFrame = false;
  bool _readyNotified = false;
  bool _appVisible = true;
  bool _showBuffering = false;
  Timer? _bufferingTimer;
  ViewerStatus? _lastStatus;
  Uri? _lastReportedSource;
  bool _preparedNotified = false;
  ViewerAdapter? _pendingValueAdapter;
  PlaybackState? _lastGoodPlayback;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    final lifecycle = WidgetsBinding.instance.lifecycleState;
    _appVisible =
        lifecycle == null ||
        lifecycle == AppLifecycleState.resumed ||
        (widget.freeView && lifecycle == AppLifecycleState.inactive);
    _create();
  }

  void _create([PlaybackState? retry]) {
    _clearBuffering();
    _hasReadyFrame = false;
    _readyNotified = false;
    _lastStatus = null;
    _lastReportedSource = null;
    _preparedNotified = false;
    _adapter = widget.factory(widget.source);
    _updates = ViewerSelection(_adapter, (value) => value);
    _presentation = ViewerSelection(_updates, (value) => value.presentation);
    _adapter.configureTransportIdentity(widget.transportIdentity);
    if (widget.continuity && widget.thumbnailSource != null) {
      _adapter.configureContinuity(widget.thumbnailSource!, widget.mapping);
    }
    widget.controller?.attach(_adapter);
    _updates.addListener(_reportStatus);
    if (!widget.visible || !_appVisible) unawaited(_adapter.pause());
    final playback = retry ?? widget.playback;
    if (playback != null) unawaited(_restore(playback, retry: retry != null));
    unawaited(_adapter.initialize());
    _reportStatus();
  }

  void _afterFrame(VoidCallback callback) {
    final binding = WidgetsBinding.instance;
    binding.addPostFrameCallback((_) => callback());
    // Hidden surfaces and position-only updates need not dirty a widget.
    // Control acknowledgements must still run even when no repaint is pending.
    binding.ensureVisualUpdate();
  }

  void _reportStatus() {
    final adapter = _adapter;
    _updateBufferingFeedback();
    final status = adapter.value.status;
    if (status == ViewerStatus.ready) {
      _lastGoodPlayback = PlaybackState(
        adapter.value.position,
        adapter.value.playIntent ?? adapter.value.playing,
      );
    }
    final reportValue =
        widget.onValueChanged != null ||
        (adapter.value.prepared &&
            adapter.value.timeline != null &&
            !_preparedNotified);
    if (reportValue && !identical(_pendingValueAdapter, adapter)) {
      _pendingValueAdapter = adapter;
      _afterFrame(() {
        if (identical(_pendingValueAdapter, adapter)) {
          _pendingValueAdapter = null;
        }
        if (!mounted || !identical(adapter, _adapter)) return;
        widget.onValueChanged?.call(adapter.value);
        if (adapter.value.prepared &&
            adapter.value.timeline != null &&
            !_preparedNotified) {
          _preparedNotified = true;
          widget.onPrepared?.call(adapter.value.timeline!);
        }
      });
    }
    if (status == ViewerStatus.ready) _hasReadyFrame = true;
    final source = adapter.value.source;
    if (status == ViewerStatus.ready &&
        source != null &&
        source != _lastReportedSource) {
      _lastReportedSource = source;
      _afterFrame(() {
        if (mounted &&
            identical(adapter, _adapter) &&
            adapter.value.source == source) {
          widget.onSourceReady?.call(source);
        }
      });
    }
    if (status == _lastStatus) return;
    _lastStatus = status;
    _afterFrame(() {
      if (!mounted ||
          !identical(adapter, _adapter) ||
          adapter.value.status != status) {
        return;
      }
      widget.onStatusChanged?.call(status);
      if (status == ViewerStatus.ready && !_readyNotified) {
        _readyNotified = true;
        widget.onReady?.call();
        if (adapter.value.source == null) {
          widget.onSourceReady?.call(widget.source);
        }
      }
    });
  }

  bool get _buffering =>
      !widget.freeView &&
      widget.visible &&
      _appVisible &&
      _adapter.value.status == ViewerStatus.ready &&
      _adapter.value.buffering;

  void _clearBuffering() {
    _bufferingTimer?.cancel();
    _bufferingTimer = null;
    _showBuffering = false;
  }

  void _updateBufferingFeedback() {
    if (!_buffering) {
      _clearBuffering();
    } else if (!_showBuffering && _bufferingTimer == null) {
      // Native startup, seeks and loop boundaries can briefly report waiting.
      // Preserve the displayed frame and only announce a sustained stall.
      _bufferingTimer = Timer(const Duration(milliseconds: 600), () {
        _bufferingTimer = null;
        if (mounted && _buffering) setState(() => _showBuffering = true);
      });
    }
  }

  @override
  void didUpdateWidget(PreviewViewer oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.visible != oldWidget.visible) _applyVisibility();
    if (widget.playback != null && widget.playback != oldWidget.playback) {
      unawaited(_restore(widget.playback!));
    }
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller?.detach(_adapter);
      widget.controller?.attach(_adapter);
    }
    if (widget.freeView &&
        (widget.source != oldWidget.source ||
            widget.sourceRevision != oldWidget.sourceRevision)) {
      unawaited(_replace(widget.source));
    }
  }

  Future<void> _restore(PlaybackState state, {bool retry = false}) async {
    final adapter = _adapter;
    final restored = await adapter.restorePlayback(state);
    if (!mounted ||
        !identical(adapter, _adapter) ||
        (!retry && widget.playback != state) ||
        !restored) {
      return;
    }
    _afterFrame(() {
      if (mounted &&
          identical(adapter, _adapter) &&
          (retry || widget.playback == state)) {
        widget.onPlaybackRestored?.call();
      }
    });
  }

  Future<void> _replace(Uri source) async {
    final adapter = _adapter;
    final ready = await adapter.updateSource(source, sha256: widget.sha256);
    if (!mounted || !identical(adapter, _adapter) || widget.source != source) {
      return;
    }
    if (ready) {
      if (adapter.value.source == null) widget.onSourceReady?.call(source);
    } else {
      widget.onSourceError?.call();
    }
  }

  void _applyVisibility() {
    _updateBufferingFeedback();
    if (widget.visible && _appVisible) {
      unawaited(_adapter.resume());
    } else {
      unawaited(_adapter.pause());
    }
  }

  void _retry() {
    setState(() {
      _updates.removeListener(_reportStatus);
      _presentation.dispose();
      _updates.dispose();
      widget.controller?.detach(_adapter);
      _adapter.dispose();
      _create(_lastGoodPlayback);
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Focusing the iframe blurs its Flutter parent without hiding the scene.
    if (widget.freeView && state == AppLifecycleState.inactive) return;
    setState(() => _appVisible = state == AppLifecycleState.resumed);
    _applyVisibility();
  }

  @override
  void dispose() {
    _bufferingTimer?.cancel();
    WidgetsBinding.instance.removeObserver(this);
    _updates.removeListener(_reportStatus);
    _presentation.dispose();
    _updates.dispose();
    widget.controller?.detach(_adapter);
    _adapter.dispose();
    _playbackFocus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ExcludeFocus(
    excluding: !widget.visible,
    child: ExcludeSemantics(
      excluding: !widget.visible,
      child: IgnorePointer(
        ignoring: !widget.visible,
        child: _buildViewer(context),
      ),
    ),
  );

  void _togglePlayback() => unawaited(_interact(_adapter.togglePlayback));

  Future<void> _interact(Future<void> Function() action) async {
    final adapter = _adapter;
    await action();
    if (mounted && identical(adapter, _adapter)) {
      widget.onPlaybackInteraction?.call(adapter.value);
    }
  }

  Widget _buildViewer(BuildContext context) => ListenableBuilder(
    listenable: _presentation,
    builder: (context, _) {
      final value = _adapter.value;
      final ready = value.status == ViewerStatus.ready;
      final canPlay = ready && value.canPlay;
      final playbackLabel = value.playing ? '一時停止' : '再生';
      return Focus(
        key: const Key('media-playback'),
        focusNode: _playbackFocus,
        canRequestFocus: canPlay,
        onKeyEvent: (_, event) {
          if (canPlay &&
              event is KeyDownEvent &&
              (event.logicalKey == LogicalKeyboardKey.space ||
                  event.logicalKey == LogicalKeyboardKey.enter)) {
            _togglePlayback();
            return KeyEventResult.handled;
          }
          return KeyEventResult.ignored;
        },
        child: Stack(
          fit: StackFit.expand,
          children: [
            if (widget.freeView) ...[
              Semantics(
                label: 'ドラッグで視点を回転できる3Dシーン',
                hint: value.canPlay ? 'ダブルタップで再生・停止。キーボードではスペースキー' : null,
                hitTestBehavior: SemanticsHitTestBehavior.transparent,
                child: _adapter.buildView(),
              ),
              if (value.status == ViewerStatus.error ||
                  (!_hasReadyFrame && !ready))
                PointerInterceptor(
                  intercepting: widget.visible,
                  child: ColoredBox(
                    color: Theme.of(context).colorScheme.surfaceContainer,
                    child: ViewerMessage(
                      icon: value.status == ViewerStatus.error
                          ? Icons.error_outline
                          : Icons.threed_rotation,
                      title: value.status == ViewerStatus.error
                          ? '自由視点を表示できませんでした'
                          : '自由視点を読み込んでいます',
                      description: '接続状況やシーンの公開状態をご確認ください。',
                      action: value.status == ViewerStatus.error
                          ? OutlinedButton.icon(
                              onPressed: _retry,
                              icon: const Icon(Icons.refresh),
                              label: const Text('再試行'),
                            )
                          : null,
                    ),
                  ),
                ),
            ] else ...[
              switch (value.status) {
                ViewerStatus.loading => const ViewerMessage(
                  icon: Icons.video_library_outlined,
                  title: '映像を読み込んでいます',
                  description: 'まもなくプレビューを表示します。',
                ),
                ViewerStatus.error => ViewerMessage(
                  icon: Icons.error_outline,
                  title: '映像を再生できませんでした',
                  description: '接続状況や映像の公開状態をご確認ください。',
                  action: OutlinedButton.icon(
                    onPressed: _retry,
                    icon: const Icon(Icons.refresh),
                    label: const Text('再試行'),
                  ),
                ),
                ViewerStatus.ready => _adapter.buildView(),
              },
              if (ready && widget.interactive)
                Positioned.fill(
                  child: PointerInterceptor(
                    child: Semantics(
                      label: '通常動画のプレビュー。$playbackLabel',
                      hint: 'タップまたはスペースキーで再生・停止',
                      button: canPlay,
                      onTap: canPlay ? _togglePlayback : null,
                      child: GestureDetector(
                        key: const Key('video-playback-surface'),
                        behavior: HitTestBehavior.opaque,
                        excludeFromSemantics: true,
                        onTap: canPlay
                            ? () {
                                _playbackFocus.requestFocus();
                                _togglePlayback();
                              }
                            : null,
                        child: !value.playing
                            ? const Center(
                                child: IgnorePointer(child: Text('タップで再生')),
                              )
                            : const SizedBox.expand(),
                      ),
                    ),
                  ),
                ),
              if (ready && _showBuffering)
                const Positioned(
                  top: 64,
                  left: 16,
                  child: IgnorePointer(child: Text('映像を読み込んでいます')),
                ),
              if (widget.showControls)
                Positioned(
                  right: 8,
                  bottom: 52,
                  child: PointerInterceptor(
                    child: IconButton.filledTonal(
                      tooltip: value.muted ? 'ミュート解除' : 'ミュート',
                      onPressed: ready
                          ? () => unawaited(_adapter.toggleMute())
                          : null,
                      icon: Icon(
                        value.muted ? Icons.volume_off : Icons.volume_up,
                      ),
                    ),
                  ),
                ),
            ],
            if (widget.showControls)
              Positioned(
                left: 0,
                right: 0,
                bottom: 0,
                child: ValueListenableBuilder<ViewerValue>(
                  valueListenable: _updates,
                  builder: (context, value, _) => EdgeSeekBar(
                    position: value.position,
                    duration: value.duration,
                    label: widget.freeView ? 'シーンの時刻' : '再生位置',
                    thumbnailSource: widget.visible
                        ? (widget.thumbnailSource ??
                              (widget.freeView ? null : widget.source))
                        : null,
                    intercepting: widget.visible,
                    onSeek:
                        value.status == ViewerStatus.ready &&
                            value.duration > Duration.zero
                        ? (position) => unawaited(
                            _interact(() => _adapter.seek(position)),
                          )
                        : null,
                    onTogglePlayback:
                        value.status == ViewerStatus.ready && value.canPlay
                        ? _togglePlayback
                        : null,
                  ),
                ),
              ),
          ],
        ),
      );
    },
  );
}

class ViewerMessage extends StatelessWidget {
  const ViewerMessage({
    required this.icon,
    required this.title,
    required this.description,
    this.action,
    super.key,
  });

  final IconData icon;
  final String title;
  final String description;
  final Widget? action;

  @override
  Widget build(BuildContext context) => Center(
    child: SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 48, color: Theme.of(context).colorScheme.outline),
          const SizedBox(height: 24),
          Text(
            title,
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.titleMedium,
          ),
          const SizedBox(height: 12),
          Text(
            description,
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
              height: 1.7,
            ),
          ),
          if (action != null) ...[const SizedBox(height: 24), action!],
        ],
      ),
    ),
  );
}
