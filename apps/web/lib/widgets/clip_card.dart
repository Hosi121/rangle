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

  void _showDetails() {
    showModalBottomSheet<void>(
      context: context,
      useSafeArea: true,
      isScrollControlled: true,
      builder: (context) => ConstrainedBox(
        constraints: BoxConstraints(
          maxHeight: MediaQuery.sizeOf(context).height * .8,
        ),
        child: SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(24, 12, 24, 32),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      '出典・詳細',
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                  ),
                  IconButton(
                    tooltip: '閉じる',
                    onPressed: () => Navigator.pop(context),
                    icon: const Icon(Icons.close),
                  ),
                ],
              ),
              const SizedBox(height: 16),
              Text(clip.title, style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 8),
              Text(clip.sport),
              const SizedBox(height: 16),
              SelectableText(
                clip.description,
                semanticsLabel: clip.description,
              ),
            ],
          ),
        ),
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
      return ColoredBox(
        color: Colors.black,
        child: Stack(
          fit: StackFit.expand,
          children: [
            if (_sceneRequested && active && clip.sceneUrl != null)
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
                color: Colors.black,
                child: _ordinaryView(compact),
              ),
            Positioned(
              left: 16,
              right: 72,
              bottom: largeText ? 152 : 112,
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
                                '${clip.sport} · $_modeLabel',
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
                  const SizedBox(height: 8),
                  Row(
                    children: [
                      if (compact)
                        IconButton.filledTonal(
                          tooltip: _modeAction,
                          onPressed: clip.sceneUrl == null ? null : _toggleMode,
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
                        onPressed: _showDetails,
                        icon: const Icon(Icons.info_outline),
                      ),
                    ],
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
