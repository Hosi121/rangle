import 'package:flutter/material.dart';

import '../models/clip.dart';
import '../viewer/viewer_adapter.dart';
import 'preview_viewer.dart';

class ClipCard extends StatefulWidget {
  const ClipCard({
    required this.clip,
    required this.active,
    required this.viewerFactory,
    required this.sceneFactory,
    required this.onFreeViewChanged,
    this.onNext,
    super.key,
  });
  final Clip clip;
  final bool active;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;
  final ValueChanged<bool> onFreeViewChanged;
  final VoidCallback? onNext;

  @override
  State<ClipCard> createState() => _ClipCardState();
}

class _ClipCardState extends State<ClipCard> {
  bool _freeView = false;
  bool _sceneRequested = false;
  bool _sceneReady = false;
  bool _pendingScene = false;
  bool _sceneError = false;
  Clip get clip => widget.clip;
  bool get active => widget.active;
  VoidCallback? get onNext => widget.onNext;

  @override
  void didUpdateWidget(ClipCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!active ||
        oldWidget.clip.id != clip.id ||
        oldWidget.clip.sceneUrl != clip.sceneUrl) {
      _freeView = false;
      _sceneRequested = false;
      _sceneReady = false;
      _pendingScene = false;
      _sceneError = false;
    }
  }

  void _toggleMode() {
    setState(() {
      if (_freeView) {
        _freeView = false;
      } else if (_pendingScene) {
        _pendingScene = false;
        _sceneRequested = false;
        _sceneReady = false;
      } else {
        _sceneRequested = true;
        _sceneError = false;
        _freeView = _sceneReady;
        _pendingScene = !_sceneReady;
      }
    });
    widget.onFreeViewChanged(_freeView);
  }

  void _onSceneReady() {
    if (!active || !_sceneRequested) return;
    final reveal = _pendingScene;
    setState(() {
      _sceneReady = true;
      _pendingScene = false;
      _freeView = reveal;
    });
    if (reveal) widget.onFreeViewChanged(true);
  }

  void _onSceneStatus(ViewerStatus status) {
    if (!active || !_sceneRequested || status != ViewerStatus.error) return;
    final wasFreeView = _freeView;
    setState(() {
      _sceneRequested = false;
      _sceneReady = false;
      _pendingScene = false;
      _freeView = false;
      _sceneError = true;
    });
    if (wasFreeView) widget.onFreeViewChanged(false);
  }

  String get _modeLabel => _sceneError
      ? '自由視点を表示できませんでした'
      : _pendingScene
      ? '通常動画・自由視点準備中'
      : _freeView
      ? '自由視点'
      : clip.previewUrl == null
      ? '映像準備中'
      : '通常動画';

  String get _modeAction => clip.sceneUrl == null
      ? '自由視点は準備中'
      : _freeView
      ? '動画に戻る'
      : _pendingScene
      ? '自由視点の準備をキャンセル'
      : _sceneError
      ? '自由視点を再試行'
      : '自由視点で見る';

  Widget _ordinaryView(bool compact) {
    if (clip.previewUrl != null && active) {
      return PreviewViewer(
        key: ValueKey('${clip.id}-video'),
        source: clip.previewUrl!,
        factory: widget.viewerFactory,
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
        if (!compact) const _PendingControls(),
      ],
    );
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final compact =
          constraints.maxHeight < 400 ||
          MediaQuery.textScalerOf(context).scale(1) > 1.4;
      final colors = Theme.of(context).colorScheme;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: colors.surfaceContainer,
                border: Border.all(color: colors.outlineVariant),
                borderRadius: BorderRadius.circular(20),
              ),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(20),
                child: Column(
                  children: [
                    if (!compact) ...[
                      Padding(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 16,
                          vertical: 12,
                        ),
                        child: Row(
                          children: [
                            Icon(
                              Icons.sports_basketball_outlined,
                              size: 18,
                              color: colors.onSurfaceVariant,
                            ),
                            const SizedBox(width: 8),
                            Expanded(
                              child: Text(
                                clip.sport,
                                style: Theme.of(context).textTheme.labelLarge,
                              ),
                            ),
                            Flexible(
                              child: Semantics(
                                liveRegion: true,
                                child: Text(
                                  _modeLabel,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                  style: Theme.of(context).textTheme.labelSmall,
                                ),
                              ),
                            ),
                          ],
                        ),
                      ),
                      const Divider(height: 1),
                    ],
                    Expanded(
                      child: Stack(
                        fit: StackFit.expand,
                        children: [
                          if (_sceneRequested &&
                              active &&
                              clip.sceneUrl != null)
                            PreviewViewer(
                              key: ValueKey('${clip.id}-scene'),
                              source: clip.sceneUrl!,
                              factory: widget.sceneFactory,
                              freeView: true,
                              visible: _freeView,
                              onReady: _onSceneReady,
                              onStatusChanged: _onSceneStatus,
                            ),
                          if (!_freeView)
                            ColoredBox(
                              key: ValueKey('${clip.id}-video-layer'),
                              color: colors.surfaceContainer,
                              child: _ordinaryView(compact),
                            ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
          Padding(
            padding: EdgeInsets.fromLTRB(
              4,
              compact ? 4 : 16,
              4,
              compact ? 0 : 12,
            ),
            child: compact
                ? Row(
                    children: [
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Semantics(
                              header: true,
                              child: Text(
                                clip.title,
                                maxLines: 2,
                                overflow: TextOverflow.ellipsis,
                                style: Theme.of(context).textTheme.titleMedium,
                              ),
                            ),
                            Semantics(
                              liveRegion: true,
                              child: Text(
                                _modeLabel,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: Theme.of(context).textTheme.labelSmall,
                              ),
                            ),
                          ],
                        ),
                      ),
                      IconButton(
                        tooltip: _modeAction,
                        onPressed: clip.sceneUrl == null ? null : _toggleMode,
                        icon: Icon(
                          _pendingScene
                              ? Icons.hourglass_top
                              : _freeView
                              ? Icons.movie_outlined
                              : Icons.threed_rotation,
                        ),
                      ),
                    ],
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Semantics(
                        header: true,
                        child: Text(
                          clip.title,
                          style: Theme.of(context).textTheme.titleLarge
                              ?.copyWith(fontWeight: FontWeight.w700),
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        clip.description,
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          height: 1.6,
                          color: colors.onSurfaceVariant,
                        ),
                      ),
                      const SizedBox(height: 12),
                      SizedBox(
                        width: double.infinity,
                        child: OutlinedButton.icon(
                          onPressed: clip.sceneUrl == null ? null : _toggleMode,
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
                    ],
                  ),
          ),
        ],
      );
    },
  );
}

class _PendingControls extends StatelessWidget {
  const _PendingControls();

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
    child: Column(
      children: [
        Semantics(
          label: '映像準備中のため再生位置を変更できません',
          child: const Slider(value: 0, onChanged: null),
        ),
        const Row(
          children: [
            IconButton.filledTonal(
              tooltip: '映像準備中のため再生できません',
              onPressed: null,
              icon: Icon(Icons.play_arrow),
            ),
            SizedBox(width: 12),
            Expanded(
              child: Text('未公開', maxLines: 1, overflow: TextOverflow.ellipsis),
            ),
          ],
        ),
      ],
    ),
  );
}
