import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../community/account_controller.dart';
import '../community/account_dialog.dart';
import '../community/cheer_controller.dart';
import '../community/clip_actions.dart';
import '../community/comments_sheet.dart';
import '../community/community_api.dart';
import '../feedback/haptics.dart' as haptics;
import '../models/clip.dart';
import '../sharing/share_clip.dart';
import '../viewer/playback_controller.dart';
import '../viewer/viewer_adapter.dart';
import '../viewer/viewer_selection.dart';
import 'cheer_seek_bar.dart';
import 'feed_sheet.dart';
import 'sports_context_details.dart';
import 'immersive_effect.dart';
import 'preview_viewer.dart';

class ClipCard extends StatefulWidget {
  const ClipCard({
    required this.clip,
    required this.active,
    required this.viewerFactory,
    required this.sceneFactory,
    required this.onFreeViewChanged,
    this.onNext,
    this.account,
    this.onTag,
    this.onQuery,
    this.onViewPublisher,
    this.shareAction = shareClip,
    this.initialFreeView = false,
    super.key,
  });
  final Clip clip;
  final bool active, initialFreeView;
  final ViewerFactory viewerFactory, sceneFactory;
  final ValueChanged<bool> onFreeViewChanged;
  final VoidCallback? onNext;
  final AccountController? account;
  final ValueChanged<String>? onTag;
  final ValueChanged<String>? onQuery;
  final ValueChanged<String>? onViewPublisher;
  final ClipShareAction shareAction;
  @override
  State<ClipCard> createState() => _ClipCardState();
}

class _ClipCardState extends State<ClipCard> with TickerProviderStateMixin {
  final _video = PlaybackController(), _scene = PlaybackController();
  late final _videoPresentation = ViewerSelection(
    _video,
    (v) => v.presentation,
  );
  late final _scenePresentation = ViewerSelection(
    _scene,
    (v) => v.presentation,
  );
  late final AnimationController _charge = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1200),
  );
  late final AnimationController _reveal = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 2850),
  );
  late final AnimationController _fire = AnimationController(
    vsync: this,
    duration: const Duration(seconds: 4),
  );
  late CheerController _cheers;
  bool _freeView = false,
      _sceneRequested = false,
      _sceneReady = false,
      _sceneError = false;
  bool _entering = false,
      _armed = false,
      _alignmentRequested = false,
      _wasPlaying = false;
  bool _hasEntered = false;
  bool _videoRequested = true;
  Duration _entryPosition = Duration.zero, _sceneTarget = Duration.zero;
  bool _captureReady = false, _pendingVideo = false;
  PlaybackState? _videoPlayback;
  ViewerTimeline? _sceneTimeline;
  int _generation = 0;
  int _cheerPulse = 0;
  String? _userId;
  Clip get clip => widget.clip;
  bool get _reduced => MediaQuery.disableAnimationsOf(context);
  PlaybackController get _current => _freeView ? _scene : _video;
  @override
  void initState() {
    super.initState();
    _freeView =
        widget.initialFreeView && widget.active && clip.freeViewUrl != null;
    _sceneRequested = _freeView;
    _videoRequested = !_freeView;
    _scene.addListener(_checkAlignment);
    _charge.addStatusListener((s) {
      if (s == AnimationStatus.completed && _entering) {
        _armed = true;
        haptics.pulse(35);
        _tryReveal();
      }
    });
    _reveal.addStatusListener((s) {
      if (s == AnimationStatus.completed && _entering) _completeEntry();
    });
    _createCheers();
    widget.account?.addListener(_accountChanged);
    _userId = widget.account?.user?.id;
  }

  void _createCheers() {
    _cheers = CheerController(
      widget.account?.api,
      clip.id,
      onError: (e) {
        if (!mounted) return;
        if (e is CommunityFailure && e.code == 'not_authenticated') {
          widget.account?.expired();
          return;
        }
        if (mounted) _message('声援は送信待ちです。概要から再送できます。');
      },
    );
    if (widget.active) unawaited(_cheers.load());
  }

  void _accountChanged() {
    final id = widget.account?.user?.id;
    if (id == _userId) return;
    _userId = id;
    _cheers.dispose();
    setState(_createCheers);
  }

  @override
  void didUpdateWidget(ClipCard old) {
    super.didUpdateWidget(old);
    if (!widget.active && old.active) unawaited(_cheers.flush());
    if (!widget.active ||
        old.clip.id != clip.id ||
        old.clip.freeViewUrl != clip.freeViewUrl ||
        old.clip.previewUrl != clip.previewUrl ||
        old.clip.revision != clip.revision) {
      final wasLocked = _freeView || _entering;
      _resetEntry();
      _freeView = false;
      _videoRequested = true;
      _sceneRequested = false;
      _sceneReady = false;
      _sceneError = false;
      _hasEntered = false;
      _sceneTimeline = null;
      _videoPlayback = null;
      _pendingVideo = false;
      if (widget.active && wasLocked) {
        final generation = _generation;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted && widget.active && generation == _generation) {
            widget.onFreeViewChanged(false);
          }
        });
      }
    }
    if (old.clip.id != clip.id) {
      _cheers.dispose();
      _createCheers();
    }
    if (widget.active && !old.active) unawaited(_cheers.load());
    if (old.account != widget.account) {
      old.account?.removeListener(_accountChanged);
      widget.account?.addListener(_accountChanged);
      _cheers.dispose();
      _createCheers();
    }
  }

  void _message(String text) {
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(text),
          behavior: SnackBarBehavior.floating,
          margin: const EdgeInsets.fromLTRB(16, 0, 16, 170),
        ),
      );
    }
  }

  void _resetEntry() {
    _generation++;
    _entering = false;
    _armed = false;
    _alignmentRequested = false;
    _captureReady = false;
    _charge.stop();
    _charge.value = 0;
    _reveal.stop();
    _reveal.value = 0;
    _fire.stop();
  }

  Future<void> _beginEntry({bool accessible = false}) async {
    if (!widget.active ||
        _freeView ||
        _entering ||
        clip.freeViewUrl == null ||
        (clip.previewUrl != null &&
            _video.value.status != ViewerStatus.ready)) {
      return;
    }
    _generation++;
    final generation = _generation;
    _entryPosition = _video.value.position;
    _wasPlaying = _video.value.playing;
    setState(() {
      _entering = true;
      _armed = false;
      _sceneError = false;
      _sceneReady = false;
      _sceneRequested = true;
      _alignmentRequested = false;
    });
    widget.onFreeViewChanged(true);
    haptics.pulse(18);
    if (!_reduced) _fire.repeat();
    _charge.forward(from: 0);
    if (accessible) _charge.value = 1;
    final captured = await _video.capture();
    if (!mounted || generation != _generation || !_entering) return;
    _entryPosition = captured.position;
    _wasPlaying = captured.playing;
    await _video.pausePlayback();
    if (!mounted || generation != _generation || !_entering) return;
    _captureReady = true;
    if (_scene.value.status == ViewerStatus.ready || _sceneTimeline != null) {
      unawaited(_alignScene());
    }
  }

  void _endHold() {
    if (_entering && !_armed) _cancelEntry();
  }

  void _cancelEntry() {
    if (!_entering) return;
    final resume = _wasPlaying;
    setState(() {
      _resetEntry();
      _sceneRequested = false;
      _sceneReady = false;
      _sceneTimeline = null;
    });
    widget.onFreeViewChanged(false);
    if (resume && widget.active) unawaited(_video.play());
  }

  Future<void> _alignScene() async {
    if (!_entering ||
        !_captureReady ||
        _alignmentRequested ||
        (_scene.value.status != ViewerStatus.ready && _sceneTimeline == null)) {
      return;
    }
    final generation = _generation;
    _alignmentRequested = true;
    final timeline = _sceneTimeline;
    final mapped = timeline?.mapping?.toScene(_entryPosition);
    _sceneTarget = timeline != null
        ? timeline.sample(mapped ?? Duration.zero)
        : Duration(
            microseconds: math.min(
              _entryPosition.inMicroseconds,
              _scene.value.duration.inMicroseconds,
            ),
          );
    final restored = await _scene.restore(PlaybackState(_sceneTarget, false));
    if (mounted && generation == _generation) {
      if (!restored) return;
      _sceneTarget = _scene.value.position;
      if (timeline == null) {
        _checkAlignment();
      } else {
        _sceneReady = true;
        _tryReveal();
      }
    }
  }

  void _onSceneReady() {
    if (!mounted || !widget.active || !_sceneRequested) return;
    if (_entering) {
      unawaited(_alignScene());
    } else if (_freeView) {
      setState(() => _sceneReady = true);
    }
  }

  void _checkAlignment() {
    // Timelined viewers acknowledge restoration only after the target is drawn.
    if (_sceneTimeline != null) return;
    if (!_entering || !_alignmentRequested || _sceneReady) return;
    final value = _scene.value;
    if (value.status == ViewerStatus.ready &&
        !value.buffering &&
        (value.position - _sceneTarget).abs() <=
            const Duration(milliseconds: 80)) {
      _sceneReady = true;
      _tryReveal();
    }
  }

  void _tryReveal() {
    if (!mounted ||
        !_entering ||
        !_armed ||
        !_sceneReady ||
        _reveal.isAnimating) {
      {
        return;
      }
    }
    if (_reduced) {
      _completeEntry();
    } else {
      _reveal.forward(from: 0);
    }
  }

  void _completeEntry() {
    final play =
        _wasPlaying &&
        (_sceneTimeline == null ||
            (_sceneTimeline!.mapping != null &&
                _sceneTimeline!.frameCount > 1));
    setState(() {
      _entering = false;
      _freeView = true;
      _hasEntered = true;
      _fire.stop();
    });
    widget.onFreeViewChanged(true);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted && _freeView && play) unawaited(_scene.play());
    });
  }

  Future<void> _back() async {
    if (!_freeView || _pendingVideo) return;
    final generation = _generation;
    final captured = await _scene.capture();
    if (!mounted || !widget.active || generation != _generation || !_freeView) {
      return;
    }
    final timeline = _sceneTimeline;
    final position = timeline == null
        ? captured.position
        : timeline.mapping?.toPreview(captured.position) ?? _entryPosition;
    final play =
        timeline == null ||
            (timeline.mapping != null && timeline.frameCount > 1)
        ? captured.playing
        : _wasPlaying;
    setState(() {
      _pendingVideo = true;
      _videoRequested = true;
      _videoPlayback = PlaybackState(position, play);
    });
  }

  void _videoRestored() {
    if (!mounted || !widget.active || !_pendingVideo) return;
    setState(() {
      _resetEntry();
      _freeView = false;
      _pendingVideo = false;
    });
    widget.onFreeViewChanged(false);
  }

  void _onSceneStatus(ViewerStatus status) {
    if (status != ViewerStatus.error || !widget.active || !_sceneRequested) {
      return;
    }
    final wasPlaying = _wasPlaying;
    setState(() {
      _resetEntry();
      _freeView = false;
      _sceneRequested = false;
      _sceneReady = false;
      _sceneError = true;
      _sceneTimeline = null;
    });
    widget.onFreeViewChanged(false);
    if (wasPlaying) unawaited(_video.play());
    _message('自由視点を表示できませんでした。通常動画を続けられます。');
  }

  Future<void> _cheer() async {
    if (_current.value.duration <= Duration.zero) return;
    if (widget.account?.user == null) {
      if (!await showAccountDialog(context, widget.account) || !mounted) return;
    }
    if (_cheers.cheer(_current.value.position, _current.value.duration)) {
      setState(() => _cheerPulse++);
      haptics.pulse(12);
    }
  }

  void _showDetails(double width) {
    showFeedSheet(
      context,
      title: '概要',
      width: width,
      builder: (context) => Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(clip.title, style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 8),
          Text(clip.sport),
          Wrap(
            spacing: 8,
            children: clip.tags
                .map(
                  (tag) => TextButton(
                    onPressed: () {
                      Navigator.pop(context);
                      widget.onTag?.call(tag);
                    },
                    child: Text('#$tag'),
                  ),
                )
                .toList(),
          ),
          const SizedBox(height: 12),
          SelectableText(clip.description, semanticsLabel: clip.description),
          if (clip.context case final details?)
            SportsContextDetails(
              context: details,
              onViewPublisher: widget.onViewPublisher == null
                  ? null
                  : (id) {
                      Navigator.pop(context);
                      widget.onViewPublisher!(id);
                    },
              onSearch: widget.onQuery == null
                  ? null
                  : (query) {
                      Navigator.pop(context);
                      widget.onQuery!(query);
                    },
            ),
          TextButton.icon(
            onPressed: () {
              Navigator.pop(context);
              showComments(this.context, clip.id, widget.account);
            },
            icon: const Icon(Icons.chat_bubble_outline),
            label: const Text('コメントを見る'),
          ),
          if (_cheers.hasPending)
            TextButton(
              onPressed: () => _cheers.flush(),
              child: const Text('送信待ちの声援を再送'),
            ),
        ],
      ),
    );
  }

  Widget _videoSurface() => Stack(
    fit: StackFit.expand,
    children: [
      PreviewViewer(
        key: ValueKey('${clip.id}-${clip.revision}-${clip.previewUrl}-video'),
        source: clip.previewUrl!,
        factory: widget.viewerFactory,
        controller: _video,
        showControls: false,
        interactive: false,
        playback: _videoPlayback,
        onPlaybackRestored: _videoRestored,
        onStatusChanged: (status) {
          if (status == ViewerStatus.error && _pendingVideo) {
            setState(() => _pendingVideo = false);
            _message('動画を表示できませんでした。もう一度戻る操作を試してください。');
          }
        },
        visible: (!_freeView || _pendingVideo) && widget.active,
      ),
      if (_video.value.status == ViewerStatus.ready)
        Positioned.fill(
          child: PointerInterceptor(
            child: GestureDetector(
              key: const Key('video-playback-surface'),
              onTap: _entering ? null : () => unawaited(_video.toggle()),
              behavior: HitTestBehavior.opaque,
              child: clip.freeViewUrl != null && !_freeView
                  ? Center(
                      child: Semantics(
                        label: '長押しで自由視点',
                        button: true,
                        onLongPress: _beginEntry,
                        child: SizedBox(
                          width: 200,
                          height: 260,
                          child: RawGestureDetector(
                            key: const Key('immersive-hold-area'),
                            behavior: HitTestBehavior.translucent,
                            gestures: {
                              LongPressGestureRecognizer:
                                  GestureRecognizerFactoryWithHandlers<
                                    LongPressGestureRecognizer
                                  >(
                                    () => LongPressGestureRecognizer(
                                      duration: const Duration(
                                        milliseconds: 280,
                                      ),
                                    ),
                                    (instance) {
                                      instance.onLongPressStart = (_) {
                                        _beginEntry();
                                      };
                                      instance.onLongPressEnd = (_) {
                                        _endHold();
                                      };
                                      instance.onLongPressCancel = _endHold;
                                      instance.onLongPressMoveUpdate = (d) {
                                        if (d.offsetFromOrigin.distance > 24 &&
                                            !_armed) {
                                          _cancelEntry();
                                        }
                                      };
                                    },
                                  ),
                            },
                          ),
                        ),
                      ),
                    )
                  : const SizedBox.expand(),
            ),
          ),
        ),
    ],
  );
  @override
  void dispose() {
    widget.account?.removeListener(_accountChanged);
    unawaited(_cheers.finish());
    _charge.dispose();
    _reveal.dispose();
    _fire.dispose();
    _scene.removeListener(_checkAlignment);
    _videoPresentation.dispose();
    _scenePresentation.dispose();
    _video.dispose();
    _scene.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final compact = constraints.maxHeight < 500;
      return AnimatedBuilder(
        animation: Listenable.merge([
          _charge,
          _reveal,
          _fire,
          _videoPresentation,
          _scenePresentation,
          _cheers,
        ]),
        builder: (context, _) {
          final current = _current.value,
              ready = current.status == ViewerStatus.ready;
          final zoom = _entering && !_reduced
              ? entryZoom(_charge.value, _reveal.value)
              : 1.0;
          return ColoredBox(
            color: Colors.black,
            child: Stack(
              fit: StackFit.expand,
              children: [
                if (_sceneRequested &&
                    widget.active &&
                    clip.freeViewUrl != null)
                  Transform.scale(
                    key: ValueKey(
                      '${clip.id}-${clip.revision}-${clip.freeViewUrl}-scene',
                    ),
                    scale: !_reduced && (_entering || _hasEntered)
                        ? entryZoom(1, 1)
                        : 1,
                    child: ExcludeFocus(
                      key: const Key('entry-scene-content'),
                      excluding: !_freeView,
                      child: ExcludeSemantics(
                        excluding: !_freeView,
                        child: IgnorePointer(
                          ignoring: !_freeView,
                          child: PreviewViewer(
                            source: clip.freeViewUrl!,
                            factory: widget.sceneFactory,
                            transportIdentity:
                                clip.manifestUrl != null &&
                                    clip.freeViewUrl == clip.manifestUrl
                                ? {
                                    'clipId': clip.id,
                                    'revision': clip.revision,
                                    'codec': clip.codec!,
                                  }
                                : null,
                            freeView: true,
                            thumbnailSource: clip.previewUrl,
                            continuity: true,
                            mapping: clip.playbackMapping,
                            // Direct result links have no video handoff to
                            // request the first frame after preparation.
                            playback: _freeView && !_hasEntered
                                ? const PlaybackState(Duration.zero, false)
                                : null,
                            onPrepared: (timeline) {
                              _sceneTimeline = timeline;
                              if (_entering) unawaited(_alignScene());
                            },
                            visible: _freeView || _entering,
                            controller: _scene,
                            showControls: false,
                            onReady: _onSceneReady,
                            onStatusChanged: _onSceneStatus,
                          ),
                        ),
                      ),
                    ),
                  ),
                if (widget.active && _videoRequested && clip.previewUrl != null)
                  Visibility(
                    key: ValueKey('${clip.id}-retained-video'),
                    visible: !_freeView,
                    maintainState: true,
                    child: Transform.scale(
                      key: const Key('entry-video-transform'),
                      scale: zoom,
                      child: ColoredBox(
                        key: ValueKey('${clip.id}-video-layer'),
                        color: Colors.black,
                        child: _videoSurface(),
                      ),
                    ),
                  ),
                if (clip.previewUrl == null && !_freeView)
                  const ViewerMessage(
                    icon: Icons.video_library_outlined,
                    title: '映像を準備しています',
                    description: '公開されたハイライトがここに表示されます。',
                  ),
                if (!_entering)
                  Positioned(
                    right: 8,
                    bottom: compact ? null : constraints.maxHeight * .38,
                    top: compact ? 72 : null,
                    child: PointerInterceptor(
                      intercepting: _freeView,
                      child: ClipActions(
                        clip: clip,
                        active: widget.active,
                        account: widget.account,
                        share: widget.shareAction,
                        compact: compact,
                        sheetWidth: constraints.maxWidth,
                        onCheer: () => unawaited(_cheer()),
                        cheers: _cheers.total,
                        cheerPulse: _cheerPulse,
                        showCommentsButton: false,
                      ),
                    ),
                  ),
                if (!_entering)
                  Positioned(
                    left: 16,
                    right: 16,
                    bottom: 106,
                    child: PointerInterceptor(
                      intercepting: _freeView,
                      child: InkWell(
                        onTap: () => _showDetails(constraints.maxWidth),
                        child: ConstrainedBox(
                          constraints: const BoxConstraints(minHeight: 48),
                          child: Row(
                            children: [
                              Flexible(
                                child: Text(
                                  clip.title,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: const TextStyle(
                                    fontSize: 12,
                                    fontWeight: FontWeight.w500,
                                  ),
                                ),
                              ),
                              if (!_freeView) ...[
                                const SizedBox(width: 8),
                                Flexible(
                                  child: Text(
                                    clip.sport,
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: const TextStyle(
                                      fontSize: 11,
                                      color: Colors.white70,
                                    ),
                                  ),
                                ),
                              ],
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                if (!_entering)
                  Positioned(
                    left: 16,
                    right: 16,
                    bottom: 52,
                    child: ValueListenableBuilder<ViewerValue>(
                      valueListenable: _current,
                      builder: (context, value, _) =>
                          value.duration > Duration.zero
                          ? CheerSeekBar(
                              bins: _cheers.bins,
                              position: value.position,
                              duration: value.duration,
                              intercepting: _freeView,
                              thumbnailSource: clip.previewUrl,
                              onTogglePlayback:
                                  value.status == ViewerStatus.ready &&
                                      value.canPlay
                                  ? () => unawaited(_current.toggle())
                                  : null,
                              onSeek: value.status == ViewerStatus.ready
                                  ? (p) => unawaited(_current.seek(p))
                                  : null,
                            )
                          : const SizedBox.shrink(),
                    ),
                  ),
                if (!_entering)
                  Positioned(
                    left: 8,
                    right: 8,
                    bottom: 4,
                    child: PointerInterceptor(
                      intercepting: _freeView,
                      child: Row(
                        children: [
                          IconButton(
                            tooltip: current.playing ? '一時停止' : '再生',
                            onPressed: ready && current.canPlay
                                ? () => unawaited(_current.toggle())
                                : null,
                            icon: Icon(
                              current.playing ? Icons.pause : Icons.play_arrow,
                            ),
                          ),
                          if (_freeView)
                            Expanded(
                              child: Align(
                                alignment: Alignment.centerLeft,
                                child: TextButton(
                                  onPressed: _back,
                                  child: const Text(
                                    '動画に戻る',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                              ),
                            )
                          else if (clip.freeViewUrl != null)
                            Expanded(
                              child: Align(
                                alignment: Alignment.centerLeft,
                                child: TextButton(
                                  onPressed: ready || clip.previewUrl == null
                                      ? () => _beginEntry(accessible: true)
                                      : null,
                                  onLongPress: ready || clip.previewUrl == null
                                      ? () => _beginEntry(accessible: true)
                                      : null,
                                  child: Text(
                                    _sceneError ? '自由視点を再試行' : '自由視点で見る',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                ),
                              ),
                            )
                          else
                            const Spacer(),
                          IconButton(
                            tooltip: '概要',
                            onPressed: () => _showDetails(constraints.maxWidth),
                            icon: const Icon(Icons.info_outline),
                          ),
                          if (!_freeView)
                            IconButton(
                              tooltip: current.muted ? 'ミュート解除' : 'ミュート',
                              onPressed: ready
                                  ? () => unawaited(_video.mute())
                                  : null,
                              icon: Icon(
                                current.muted
                                    ? Icons.volume_off
                                    : Icons.volume_up,
                              ),
                            ),
                        ],
                      ),
                    ),
                  ),
                if (_entering)
                  ImmersiveEffect(
                    charge: _charge.value,
                    reveal: _reveal.value,
                    time: _fire.value * 4,
                    reducedMotion: _reduced,
                    waiting: _armed && !_sceneReady,
                  ),
                if (_entering)
                  Positioned(
                    right: 12,
                    bottom: 12,
                    child: TextButton(
                      onPressed: _cancelEntry,
                      child: const Text('キャンセル'),
                    ),
                  ),
              ],
            ),
          );
        },
      );
    },
  );
}
