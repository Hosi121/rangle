import 'package:flutter/material.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../models/clip.dart';
import '../community/account_controller.dart';
import '../community/clip_actions.dart';
import '../sharing/share_clip.dart';
import '../viewer/viewer_adapter.dart';
import 'feed_sheet.dart';
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
    this.shareAction = shareClip,
    this.initialFreeView = false,
    super.key,
  });
  final Clip clip;
  final bool active;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;
  final ValueChanged<bool> onFreeViewChanged;
  final VoidCallback? onNext;
  final AccountController? account;
  final ValueChanged<String>? onTag;
  final ClipShareAction shareAction;
  final bool initialFreeView;

  @override
  State<ClipCard> createState() => _ClipCardState();
}

class _ClipCardState extends State<ClipCard> {
  bool _freeView = false;
  bool _sceneRequested = false;
  bool _sceneReady = false;
  bool _pendingScene = false;
  bool _sceneError = false;
  bool _pendingVideo = false, _videoError = false;
  int _clipEpoch = 0, _sceneEpoch = 0, _videoAttempt = 0;
  PlaybackState _playback = const PlaybackState(Duration.zero, false);
  PlaybackState? _scenePlayback, _videoPlayback;
  ViewerTimeline? _sceneTimeline;
  bool _timeClamped = false;
  ViewerAdapter? _videoAdapter, _sceneAdapter;
  int _sceneCapture = 0, _returnCapture = 0;
  Clip get clip => widget.clip;
  bool get active => widget.active;
  VoidCallback? get onNext => widget.onNext;

  @override
  void initState() {
    super.initState();
    _freeView = widget.initialFreeView && active && clip.sceneUrl != null;
    _sceneRequested = _freeView;
  }

  @override
  void didUpdateWidget(ClipCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!active ||
        oldWidget.clip.id != clip.id ||
        oldWidget.clip.sceneUrl != clip.sceneUrl ||
        oldWidget.clip.previewUrl != clip.previewUrl ||
        oldWidget.clip.revision != clip.revision) {
      final wasFreeView = _freeView;
      _clipEpoch++;
      _sceneEpoch++;
      _playback = const PlaybackState(Duration.zero, false);
      _scenePlayback = _videoPlayback = null;
      _sceneTimeline = null;
      _timeClamped = false;
      _pendingVideo = _videoError = false;
      _freeView = false;
      _sceneRequested = false;
      _sceneReady = false;
      _pendingScene = false;
      _sceneError = false;
      _videoAdapter = _sceneAdapter = null;
      if (active && wasFreeView) {
        final epoch = _clipEpoch;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted && active && epoch == _clipEpoch) {
            widget.onFreeViewChanged(false);
          }
        });
      }
    }
  }

  Future<void> _toggleMode() async {
    if (_freeView &&
        !_pendingVideo &&
        clip.previewUrl != null &&
        _sceneAdapter != null) {
      final epoch = _clipEpoch,
          sceneEpoch = _sceneEpoch,
          request = ++_returnCapture;
      final adapter = _sceneAdapter!;
      final captured = await adapter.capturePlayback();
      if (!mounted ||
          !active ||
          epoch != _clipEpoch ||
          sceneEpoch != _sceneEpoch ||
          !identical(adapter, _sceneAdapter) ||
          request != _returnCapture ||
          !_freeView) {
        return;
      }
      final mapping = _sceneTimeline?.mapping;
      if (mapping != null) {
        _playback = PlaybackState(
          mapping.toPreview(captured.position),
          _sceneTimeline!.frameCount > 1 ? captured.playing : _playback.playing,
        );
      }
    }
    setState(() {
      if (_freeView) {
        if (clip.previewUrl == null) {
          _freeView = false;
        } else if (_pendingVideo) {
          _pendingVideo = false;
        } else {
          _pendingVideo = true;
          _videoError = false;
          _videoAttempt++;
          _videoPlayback = PlaybackState(_playback.position, _playback.playing);
        }
      } else if (_pendingScene) {
        _pendingScene = false;
        _sceneRequested = false;
        _sceneReady = false;
        _sceneEpoch++;
        _sceneTimeline = null;
        _scenePlayback = null;
      } else {
        _sceneRequested = true;
        _sceneError = false;
        _pendingScene = true;
        if (_sceneReady && _sceneTimeline == null) {
          _freeView = true;
          _pendingScene = false;
        }
      }
    });
    if (_pendingScene && _sceneTimeline != null) _syncScene(force: true);
    widget.onFreeViewChanged(_freeView);
  }

  void _onSceneReady(int epoch) {
    if (!active || !_sceneRequested || epoch != _sceneEpoch) return;
    final reveal = _pendingScene || _freeView;
    setState(() {
      _sceneReady = true;
      _pendingScene = false;
      _freeView = reveal;
      final mapping = _sceneTimeline?.mapping;
      if (mapping != null && _scenePlayback != null) {
        _playback = PlaybackState(
          mapping.toPreview(_scenePlayback!.position),
          _sceneTimeline!.frameCount > 1
              ? _scenePlayback!.playing
              : _playback.playing,
        );
      }
    });
    if (reveal) widget.onFreeViewChanged(true);
  }

  void _onSceneStatus(ViewerStatus status, int epoch) {
    if (!active ||
        !_sceneRequested ||
        epoch != _sceneEpoch ||
        status != ViewerStatus.error) {
      return;
    }
    final wasFreeView = _freeView;
    setState(() {
      _sceneRequested = false;
      _sceneReady = false;
      _pendingScene = false;
      _freeView = false;
      _sceneError = true;
      _sceneEpoch++;
      _sceneTimeline = null;
      _scenePlayback = null;
      if (wasFreeView) {
        _videoPlayback = PlaybackState(_playback.position, _playback.playing);
      }
      _pendingVideo = false;
    });
    if (wasFreeView) widget.onFreeViewChanged(false);
  }

  void _videoValue(ViewerValue value, int epoch) {
    if (epoch != _clipEpoch ||
        !active ||
        _freeView ||
        value.status != ViewerStatus.ready) {
      return;
    }
    final intent = value.playIntent ?? value.playing;
    final changedIntent = intent != _playback.playing;
    _playback = PlaybackState(value.position, intent);
    // Automatic progress must not starve a slow first sort. Explicit user
    // seeks and play/pause actions may supersede the latched handoff below.
    if (changedIntent && _pendingScene && _sceneTimeline != null) _syncScene();
  }

  void _sceneValue(ViewerValue value, int epoch) {
    final mapping = _sceneTimeline?.mapping;
    if (epoch != _sceneEpoch ||
        !active ||
        !_freeView ||
        _pendingVideo ||
        value.status != ViewerStatus.ready ||
        value.buffering ||
        mapping == null) {
      return;
    }
    _playback = PlaybackState(
      mapping.toPreview(value.position),
      value.canPlay ? value.playIntent ?? value.playing : _playback.playing,
    );
  }

  Future<void> _syncScene({bool force = false}) async {
    final epoch = _clipEpoch,
        sceneEpoch = _sceneEpoch,
        request = ++_sceneCapture;
    if (_videoAdapter != null && !_freeView) {
      final captured = await _videoAdapter!.capturePlayback();
      if (!mounted ||
          !active ||
          epoch != _clipEpoch ||
          sceneEpoch != _sceneEpoch ||
          request != _sceneCapture ||
          !_sceneRequested ||
          !_pendingScene) {
        return;
      }
      _playback = captured;
    }
    final timeline = _sceneTimeline!;
    final mapping = timeline.mapping;
    final mapped = mapping?.toScene(_playback.position) ?? Duration.zero;
    _timeClamped =
        mapping != null &&
        (mapped < Duration.zero ||
            mapped > timeline.atFrame(timeline.frameCount - 1));
    final position = mapping == null
        ? Duration.zero
        : timeline.sample(mapping.toScene(_playback.position));
    final playing =
        mapping != null && timeline.frameCount > 1 && _playback.playing;
    if (!force &&
        _scenePlayback?.position == position &&
        _scenePlayback?.playing == playing) {
      return;
    }
    setState(() => _scenePlayback = PlaybackState(position, playing));
  }

  void _videoRestored(int epoch) {
    if (epoch != _clipEpoch || !_pendingVideo || !active) return;
    setState(() {
      _freeView = false;
      _pendingVideo = false;
    });
    widget.onFreeViewChanged(false);
  }

  String get _modeLabel => _sceneError
      ? '自由視点を表示できませんでした'
      : _videoError
      ? '動画を表示できませんでした'
      : _pendingScene
      ? '通常動画・自由視点準備中'
      : _freeView
      ? _sceneTimeline != null && _sceneTimeline!.mapping == null
            ? '自由視点・時刻の対応情報なし'
            : _timeClamped
            ? '自由視点・${(_scenePlayback!.position.inMicroseconds / 1000000).toStringAsFixed(2)}秒へ調整'
            : '自由視点'
      : clip.previewUrl == null
      ? '映像準備中'
      : '通常動画';

  String get _modeAction => clip.sceneUrl == null
      ? '自由視点は準備中'
      : _freeView
      ? _pendingVideo
            ? '動画の準備をキャンセル'
            : _videoError
            ? '動画を再試行'
            : '動画に戻る'
      : _pendingScene
      ? '自由視点の準備をキャンセル'
      : _sceneError
      ? '自由視点を再試行'
      : '自由視点で見る';

  Widget _ordinaryView(bool compact) {
    if (clip.previewUrl != null && active) {
      final epoch = _clipEpoch;
      return PreviewViewer(
        key: ValueKey('${clip.id}-$_clipEpoch-video-$_videoAttempt'),
        source: clip.previewUrl!,
        factory: (source) => _videoAdapter = widget.viewerFactory(source),
        playback: _videoPlayback,
        onValueChanged: (value) => _videoValue(value, epoch),
        onPlaybackInteraction: (value) {
          _videoValue(value, epoch);
          if (epoch == _clipEpoch && _pendingScene && _sceneTimeline != null) {
            _syncScene();
          }
        },
        onPlaybackRestored: () => _videoRestored(epoch),
        onStatusChanged: (status) {
          if (epoch == _clipEpoch &&
              active &&
              _pendingVideo &&
              status == ViewerStatus.error) {
            setState(() {
              _pendingVideo = false;
              _videoError = true;
            });
          }
        },
      );
    }
    return Column(
      children: [
        Expanded(
          child: compact
              ? Center(
                  child: Text(
                    '映像を準備しています',
                    style: Theme.of(context).textTheme.titleSmall,
                  ),
                )
              : ViewerMessage(
                  icon: Icons.video_library_outlined,
                  title: '映像を準備しています',
                  description: '公開されたハイライトが\nここに表示されます。',
                  action: onNext == null
                      ? null
                      : OutlinedButton.icon(
                          onPressed: onNext,
                          icon: const Icon(Icons.arrow_downward, size: 18),
                          label: const Text('次の項目を見る'),
                        ),
                ),
        ),
      ],
    );
  }

  Widget _videoLayer(bool compact) => ColoredBox(
    key: ValueKey('${clip.id}-$_clipEpoch-video-layer'),
    color: Colors.black,
    child: _ordinaryView(compact),
  );

  void _showDetails(double width) {
    showFeedSheet(
      context,
      title: '出典・詳細',
      width: width,
      builder: (context) => Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(clip.title, style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 8),
          Text(clip.sport),
          if (clip.tags.isNotEmpty)
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
          const SizedBox(height: 16),
          SelectableText(clip.description, semanticsLabel: clip.description),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final largeText = MediaQuery.textScalerOf(context).scale(1) > 1.4;
      final compact =
          constraints.maxHeight < 500 ||
          constraints.maxWidth < 360 ||
          largeText;
      final sceneEpoch = _sceneEpoch;
      return ColoredBox(
        color: Colors.black,
        child: Stack(
          fit: StackFit.expand,
          children: [
            if (_pendingVideo) _videoLayer(compact),
            if (_sceneRequested && active && clip.sceneUrl != null)
              PreviewViewer(
                key: ValueKey('${clip.id}-$_clipEpoch-scene-$sceneEpoch'),
                source: clip.sceneUrl!,
                factory: (source) =>
                    _sceneAdapter = widget.sceneFactory(source),
                freeView: true,
                thumbnailSource: clip.previewUrl,
                continuity: true,
                mapping: clip.playbackMapping,
                playback: _scenePlayback,
                visible: _freeView,
                onReady: () {
                  if (_sceneTimeline == null) _onSceneReady(sceneEpoch);
                },
                onPrepared: (timeline) {
                  if (sceneEpoch != _sceneEpoch || !_sceneRequested) return;
                  _sceneTimeline = timeline;
                  _syncScene();
                },
                onPlaybackRestored: () => _onSceneReady(sceneEpoch),
                onValueChanged: (value) => _sceneValue(value, sceneEpoch),
                onStatusChanged: (status) => _onSceneStatus(status, sceneEpoch),
              ),
            if (!_freeView) _videoLayer(compact),
            Positioned(
              right: compact ? 72 : 8,
              top: compact ? 64 : null,
              bottom: compact ? null : constraints.maxHeight * .36,
              child: PointerInterceptor(
                intercepting: _freeView,
                child: ClipActions(
                  clip: clip,
                  active: active,
                  account: widget.account,
                  share: widget.shareAction,
                  compact: compact,
                  sheetWidth: constraints.maxWidth,
                ),
              ),
            ),
            Positioned(
              left: 16,
              right: 72,
              bottom: largeText ? 104 : 64,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  IgnorePointer(
                    child: DecoratedBox(
                      decoration: BoxDecoration(
                        color: Colors.black.withValues(alpha: .72),
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: Padding(
                        padding: const EdgeInsets.all(8),
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Semantics(
                              header: true,
                              child: Text(
                                clip.title,
                                maxLines: compact ? 1 : 2,
                                overflow: TextOverflow.ellipsis,
                                style: Theme.of(context).textTheme.titleMedium
                                    ?.copyWith(fontWeight: FontWeight.w700),
                              ),
                            ),
                            const SizedBox(height: 4),
                            Semantics(
                              liveRegion: true,
                              child: Text(
                                _freeView &&
                                        (_timeClamped ||
                                            (_sceneTimeline != null &&
                                                _sceneTimeline!.mapping ==
                                                    null))
                                    ? _modeLabel
                                    : '${clip.sport} · $_modeLabel',
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: Theme.of(context).textTheme.labelSmall,
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                  ),
                  if (!compact && clip.tags.isNotEmpty)
                    PointerInterceptor(
                      intercepting: _freeView,
                      child: Wrap(
                        spacing: 4,
                        children: clip.tags
                            .take(2)
                            .map(
                              (tag) => TextButton(
                                style: TextButton.styleFrom(
                                  minimumSize: const Size(48, 48),
                                  padding: const EdgeInsets.symmetric(
                                    horizontal: 8,
                                  ),
                                  backgroundColor: Colors.black.withValues(
                                    alpha: .55,
                                  ),
                                ),
                                onPressed: () => widget.onTag?.call(tag),
                                child: Text(
                                  '#$tag',
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            )
                            .toList(),
                      ),
                    ),
                  const SizedBox(height: 8),
                  PointerInterceptor(
                    intercepting: _freeView,
                    child: Row(
                      children: [
                        if (compact)
                          IconButton.filledTonal(
                            tooltip: _modeAction,
                            onPressed: clip.sceneUrl == null
                                ? null
                                : _toggleMode,
                            icon: Icon(
                              _pendingScene
                                  ? Icons.hourglass_top
                                  : _freeView
                                  ? Icons.movie_outlined
                                  : Icons.threed_rotation,
                            ),
                          )
                        else
                          Flexible(
                            child: FilledButton.tonalIcon(
                              onPressed: clip.sceneUrl == null
                                  ? null
                                  : _toggleMode,
                              icon: Icon(
                                _pendingScene
                                    ? Icons.hourglass_top
                                    : _freeView
                                    ? Icons.movie_outlined
                                    : Icons.threed_rotation,
                                size: 18,
                              ),
                              label: Text(_modeAction),
                            ),
                          ),
                        const SizedBox(width: 8),
                        IconButton.filledTonal(
                          tooltip: '出典・詳細',
                          onPressed: () => _showDetails(constraints.maxWidth),
                          icon: const Icon(Icons.info_outline),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ],
        ),
      );
    },
  );
}
