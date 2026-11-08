import 'dart:async';
import 'dart:ui' show SemanticsHitTestBehavior;

import 'package:flutter/cupertino.dart' show CupertinoSlider;
import 'package:flutter/material.dart';

import '../viewer/viewer_adapter.dart';

class PreviewViewer extends StatefulWidget {
  const PreviewViewer({
    required this.source,
    required this.factory,
    this.freeView = false,
    this.visible = true,
    this.onReady,
    this.onStatusChanged,
    super.key,
  });

  final Uri source;
  final ViewerFactory factory;
  final bool freeView;

  /// Retains layout while pausing rendering and excluding hidden controls.
  /// The host paints its visible content above this surface when false.
  final bool visible;
  final VoidCallback? onReady;
  final ValueChanged<ViewerStatus>? onStatusChanged;

  @override
  State<PreviewViewer> createState() => _PreviewViewerState();
}

class _PreviewViewerState extends State<PreviewViewer>
    with WidgetsBindingObserver {
  late ViewerAdapter _adapter;
  bool _hasReadyFrame = false;
  bool _readyNotified = false;
  bool _appVisible = true;
  ViewerStatus? _lastStatus;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _create();
  }

  void _create() {
    _hasReadyFrame = false;
    _readyNotified = false;
    _lastStatus = null;
    _adapter = widget.factory(widget.source);
    _adapter.addListener(_reportStatus);
    if (!widget.visible || !_appVisible) unawaited(_adapter.pause());
    unawaited(_adapter.initialize());
    _reportStatus();
  }

  void _reportStatus() {
    final adapter = _adapter;
    final status = adapter.value.status;
    if (status == ViewerStatus.ready) _hasReadyFrame = true;
    if (status == _lastStatus) return;
    _lastStatus = status;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted ||
          !identical(adapter, _adapter) ||
          adapter.value.status != status) {
        return;
      }
      widget.onStatusChanged?.call(status);
      if (status == ViewerStatus.ready && !_readyNotified) {
        _readyNotified = true;
        widget.onReady?.call();
      }
    });
  }

  @override
  void didUpdateWidget(PreviewViewer oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.visible != oldWidget.visible) _applyVisibility();
  }

  void _applyVisibility() {
    if (widget.visible && _appVisible) {
      unawaited(_adapter.resume());
    } else {
      unawaited(_adapter.pause());
    }
  }

  void _retry() {
    setState(() {
      _adapter.removeListener(_reportStatus);
      _adapter.dispose();
      _create();
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Focusing the iframe blurs its Flutter parent without hiding the scene.
    if (widget.freeView && state == AppLifecycleState.inactive) return;
    _appVisible = state == AppLifecycleState.resumed;
    _applyVisibility();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _adapter.removeListener(_reportStatus);
    _adapter.dispose();
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

  Widget _buildViewer(BuildContext context) => ListenableBuilder(
    listenable: _adapter,
    builder: (context, _) {
      final value = _adapter.value;
      final ready = value.status == ViewerStatus.ready;
      final showPlayback = !widget.freeView || value.canPlay;
      final touchHint = value.canPlay ? 'ドラッグで視点操作、ダブルタップで再生・停止' : 'ドラッグで視点操作';
      return Stack(
        fit: StackFit.expand,
        children: [
          widget.freeView
              ? Stack(
                  fit: StackFit.expand,
                  children: [
                    Semantics(
                      label: 'ドラッグで視点を回転できる3Dシーン',
                      hint: value.canPlay ? 'ダブルタップで再生・停止' : null,
                      hitTestBehavior: SemanticsHitTestBehavior.transparent,
                      child: _adapter.buildView(),
                    ),
                    if (value.status == ViewerStatus.error ||
                        (!_hasReadyFrame && !ready))
                      ColoredBox(
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
                  ],
                )
              : switch (value.status) {
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
                  ViewerStatus.ready => Semantics(
                    label: '通常動画のプレビュー',
                    child: _adapter.buildView(),
                  ),
                },
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: ColoredBox(
              color: Colors.black.withValues(alpha: .78),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
                child: Column(
                  children: [
                    if (ready && value.buffering && !widget.freeView)
                      const Text('映像を読み込んでいます'),
                    Semantics(
                      label: widget.freeView ? 'シーンの時刻' : '再生位置',
                      // Material Slider's value-indicator semantics overlay blocks
                      // this embedded viewer on Flutter 3.47.
                      child: widget.freeView
                          ? SizedBox(
                              width: double.infinity,
                              child: CupertinoSlider(
                                key: const Key('media-scrubber'),
                                activeColor: Theme.of(context)
                                    .colorScheme
                                    .primary,
                                value: ready
                                    ? value.position.inMilliseconds
                                          .toDouble()
                                          .clamp(
                                            0,
                                            value.duration.inMilliseconds
                                                .toDouble(),
                                          )
                                    : 0,
                                max: value.duration.inMilliseconds > 0
                                    ? value.duration.inMilliseconds.toDouble()
                                    : 1,
                                onChanged:
                                    ready && value.duration > Duration.zero
                                    ? (value) => unawaited(
                                        _adapter.seek(
                                          Duration(milliseconds: value.round()),
                                        ),
                                      )
                                    : null,
                              ),
                            )
                          : Slider(
                              key: const Key('media-scrubber'),
                              value: ready
                                  ? value.position.inMilliseconds
                                        .toDouble()
                                        .clamp(
                                          0,
                                          value.duration.inMilliseconds
                                              .toDouble(),
                                        )
                                  : 0,
                              max: value.duration.inMilliseconds > 0
                                  ? value.duration.inMilliseconds.toDouble()
                                  : 1,
                              onChanged: ready && value.duration > Duration.zero
                                  ? (value) => unawaited(
                                      _adapter.seek(
                                        Duration(milliseconds: value.round()),
                                      ),
                                    )
                                  : null,
                              semanticFormatterCallback: (value) => _formatTime(
                                Duration(milliseconds: value.round()),
                              ),
                            ),
                    ),
                    Row(
                      children: [
                        if (showPlayback)
                          IconButton.filledTonal(
                            tooltip: value.playing ? '一時停止' : '再生',
                            onPressed: ready && value.canPlay
                                ? () => unawaited(_adapter.togglePlayback())
                                : null,
                            icon: Icon(
                              value.playing ? Icons.pause : Icons.play_arrow,
                            ),
                          ),
                        if (showPlayback) const SizedBox(width: 12),
                        Expanded(
                          child: Text(
                            '${_formatTime(value.position)} / ${_formatTime(value.duration)}',
                            style: const TextStyle(
                              fontFeatures: [FontFeature.tabularFigures()],
                            ),
                          ),
                        ),
                        if (widget.freeView)
                          Tooltip(
                            message: touchHint,
                            child: Icon(
                              Icons.touch_app_outlined,
                              semanticLabel: touchHint,
                            ),
                          ),
                        if (!widget.freeView)
                          IconButton(
                            tooltip: value.muted ? 'ミュート解除' : 'ミュート',
                            onPressed: ready
                                ? () => unawaited(_adapter.toggleMute())
                                : null,
                            icon: Icon(
                              value.muted ? Icons.volume_off : Icons.volume_up,
                            ),
                          ),
                      ],
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      );
    },
  );
}

String _formatTime(Duration time) {
  final seconds = time.inSeconds.clamp(0, 359999);
  if (time.inMilliseconds < 1000) {
    return '${(time.inMilliseconds.clamp(0, 999) / 1000).toStringAsFixed(2)}秒';
  }
  return '${seconds ~/ 60}:${(seconds % 60).toString().padLeft(2, '0')}';
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
