import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'data/clip_repository.dart';
import 'models/clip.dart';
import 'viewer/viewer_adapter.dart';
import 'widgets/clip_card.dart';
import 'widgets/preview_viewer.dart';

export 'viewer/viewer_adapter.dart';

class ClipFeedScreen extends StatefulWidget {
  const ClipFeedScreen({
    required this.repository,
    required this.viewerFactory,
    required this.sceneFactory,
    super.key,
  });
  final ClipRepository repository;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;

  @override
  State<ClipFeedScreen> createState() => _ClipFeedScreenState();
}

class _ClipFeedScreenState extends State<ClipFeedScreen> {
  late Future<List<Clip>> _clips;

  @override
  void initState() {
    super.initState();
    _clips = widget.repository.load();
  }

  void _reload() {
    final clips = widget.repository.load();
    setState(() {
      _clips = clips;
    });
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    backgroundColor: Colors.black,
    body: SafeArea(
      child: FutureBuilder<List<Clip>>(
        future: _clips,
        builder: (context, snapshot) {
          if (snapshot.connectionState != ConnectionState.done) {
            return const ViewerMessage(
              icon: Icons.view_agenda_outlined,
              title: 'ハイライトを読み込んでいます',
              description: 'しばらくお待ちください。',
            );
          }
          if (snapshot.hasError) {
            return ViewerMessage(
              icon: Icons.cloud_off_outlined,
              title: 'ハイライトを読み込めませんでした',
              description: '接続状況をご確認のうえ、もう一度お試しください。',
              action: FilledButton.icon(
                onPressed: _reload,
                icon: const Icon(Icons.refresh),
                label: const Text('再読み込み'),
              ),
            );
          }
          final clips = snapshot.data ?? const <Clip>[];
          if (clips.isEmpty) {
            return ViewerMessage(
              icon: Icons.video_library_outlined,
              title: '公開されたハイライトはまだありません',
              description: '新しい映像が公開されると、ここに表示されます。',
              action: OutlinedButton.icon(
                onPressed: _reload,
                icon: const Icon(Icons.refresh),
                label: const Text('更新する'),
              ),
            );
          }
          return _HighlightFeed(
            key: ObjectKey(_clips),
            clips: clips,
            viewerFactory: widget.viewerFactory,
            sceneFactory: widget.sceneFactory,
          );
        },
      ),
    ),
  );
}

class _MoveClipIntent extends Intent {
  const _MoveClipIntent(this.delta);
  final int delta;
}

class _HighlightFeed extends StatefulWidget {
  const _HighlightFeed({
    required this.clips,
    required this.viewerFactory,
    required this.sceneFactory,
    super.key,
  });
  final List<Clip> clips;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;

  @override
  State<_HighlightFeed> createState() => _HighlightFeedState();
}

class _HighlightFeedState extends State<_HighlightFeed> {
  final PageController _pages = PageController();
  int _selected = 0;
  bool _freeView = false;

  void _select(int index) {
    if (index < 0 || index >= widget.clips.length || index == _selected) return;
    _pages.jumpToPage(index);
  }

  @override
  void dispose() {
    _pages.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Shortcuts(
    shortcuts: const <ShortcutActivator, Intent>{
      SingleActivator(LogicalKeyboardKey.arrowDown): _MoveClipIntent(1),
      SingleActivator(LogicalKeyboardKey.arrowUp): _MoveClipIntent(-1),
      SingleActivator(LogicalKeyboardKey.pageDown): _MoveClipIntent(1),
      SingleActivator(LogicalKeyboardKey.pageUp): _MoveClipIntent(-1),
    },
    child: Actions(
      actions: <Type, Action<Intent>>{
        _MoveClipIntent: CallbackAction<_MoveClipIntent>(
          onInvoke: (intent) {
            _select(_selected + intent.delta);
            return null;
          },
        ),
      },
      child: Focus(
        autofocus: true,
        child: LayoutBuilder(
          builder: (context, constraints) {
            final desktop =
                constraints.maxWidth >= 700 && constraints.maxHeight >= 600;
            final width = desktop
                ? math.min(520.0, constraints.maxHeight * 9 / 16)
                : constraints.maxWidth;
            return Center(
              child: SizedBox(
                key: const Key('feed-player'),
                width: width,
                height: constraints.maxHeight,
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    PageView.builder(
                      key: const Key('highlight-feed'),
                      controller: _pages,
                      scrollDirection: Axis.vertical,
                      physics: _freeView
                          ? const NeverScrollableScrollPhysics()
                          : null,
                      itemCount: widget.clips.length,
                      onPageChanged: (index) => setState(() {
                        _selected = index;
                        _freeView = false;
                      }),
                      itemBuilder: (context, index) => ExcludeSemantics(
                        excluding: index != _selected,
                        child: ClipCard(
                          clip: widget.clips[index],
                          active: index == _selected,
                          viewerFactory: widget.viewerFactory,
                          sceneFactory: widget.sceneFactory,
                          onFreeViewChanged: (value) =>
                              setState(() => _freeView = value),
                          onNext: index < widget.clips.length - 1
                              ? () => _select(index + 1)
                              : null,
                        ),
                      ),
                    ),
                    Positioned(
                      top: 12,
                      left: 16,
                      right: 16,
                      child: IgnorePointer(
                        child: Row(
                          children: [
                            Expanded(
                              child: Semantics(
                                header: true,
                                label: 'rangle リプレイ アングル',
                                child: ExcludeSemantics(
                                  child: Text(
                                    'rangle',
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                    style: Theme.of(context)
                                        .textTheme
                                        .titleMedium
                                        ?.copyWith(fontWeight: FontWeight.w800),
                                  ),
                                ),
                              ),
                            ),
                            Semantics(
                              liveRegion: true,
                              label:
                                  '${widget.clips.length}件中${_selected + 1}件目',
                              child: ExcludeSemantics(
                                child: Text(
                                  '${(_selected + 1).toString().padLeft(2, '0')} / ${widget.clips.length.toString().padLeft(2, '0')}',
                                  style: Theme.of(context).textTheme.labelMedium
                                      ?.copyWith(
                                        fontFeatures: const [
                                          FontFeature.tabularFigures(),
                                        ],
                                      ),
                                ),
                              ),
                            ),
                          ],
                        ),
                      ),
                    ),
                    Positioned(
                      right: 8,
                      top: constraints.maxHeight * .4,
                      child: Column(
                        children: [
                          IconButton.filledTonal(
                            tooltip: '前のハイライト',
                            onPressed: _selected > 0
                                ? () => _select(_selected - 1)
                                : null,
                            icon: const Icon(Icons.keyboard_arrow_up),
                          ),
                          const SizedBox(height: 8),
                          IconButton.filledTonal(
                            tooltip: '次のハイライト',
                            onPressed: _selected < widget.clips.length - 1
                                ? () => _select(_selected + 1)
                                : null,
                            icon: const Icon(Icons.keyboard_arrow_down),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            );
          },
        ),
      ),
    ),
  );
}
