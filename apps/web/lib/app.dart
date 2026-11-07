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
    body: SafeArea(
      child: Column(
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 18),
            child: Row(
              children: [
                Semantics(
                  header: true,
                  label: 'rangle リプレイ アングル',
                  child: ExcludeSemantics(
                    child: SizedBox(
                      width: 110,
                      child: FittedBox(
                        fit: BoxFit.scaleDown,
                        alignment: Alignment.centerLeft,
                        child: Text(
                          'rangle',
                          style: Theme.of(context).textTheme.headlineMedium
                              ?.copyWith(fontWeight: FontWeight.w800),
                        ),
                      ),
                    ),
                  ),
                ),
                const SizedBox(width: 18),
                const Expanded(
                  child: Text(
                    'ハイライト',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                const _DemoBadge(),
              ],
            ),
          ),
          const Divider(height: 1),
          Expanded(
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
        ],
      ),
    ),
  );
}

class _DemoBadge extends StatelessWidget {
  const _DemoBadge();

  @override
  Widget build(BuildContext context) => DecoratedBox(
    decoration: BoxDecoration(
      border: Border.all(color: Theme.of(context).colorScheme.outlineVariant),
      borderRadius: BorderRadius.circular(8),
    ),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      child: Text('デモ', style: Theme.of(context).textTheme.labelMedium),
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
            final wide = constraints.maxWidth >= 1000;
            final compact = constraints.maxWidth < 600;
            return Padding(
              padding: EdgeInsets.fromLTRB(
                compact ? 12 : 32,
                16,
                compact ? 12 : 32,
                8,
              ),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (wide) ...[
                    SizedBox(
                      width: 240,
                      child: SingleChildScrollView(
                        child: _Introduction(
                          hasFreeView: widget.clips.any(
                            (clip) => clip.sceneUrl != null,
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(width: 40),
                  ],
                  Flexible(
                    child: ConstrainedBox(
                      constraints: const BoxConstraints(maxWidth: 480),
                      child: Column(
                        children: [
                          Expanded(
                            child: PageView.builder(
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
                          ),
                          const SizedBox(height: 8),
                          Row(
                            children: [
                              IconButton.outlined(
                                tooltip: '前のハイライト',
                                onPressed: _selected > 0
                                    ? () => _select(_selected - 1)
                                    : null,
                                icon: const Icon(Icons.keyboard_arrow_up),
                              ),
                              Expanded(
                                child: Semantics(
                                  liveRegion: true,
                                  label:
                                      '${widget.clips.length}件中${_selected + 1}件目',
                                  child: ExcludeSemantics(
                                    child: Text(
                                      '${(_selected + 1).toString().padLeft(2, '0')} / ${widget.clips.length.toString().padLeft(2, '0')}',
                                      textAlign: TextAlign.center,
                                      style: const TextStyle(
                                        fontFeatures: [
                                          FontFeature.tabularFigures(),
                                        ],
                                      ),
                                    ),
                                  ),
                                ),
                              ),
                              IconButton.outlined(
                                tooltip: '次のハイライト',
                                onPressed: _selected < widget.clips.length - 1
                                    ? () => _select(_selected + 1)
                                    : null,
                                icon: const Icon(Icons.keyboard_arrow_down),
                              ),
                            ],
                          ),
                          Padding(
                            padding: const EdgeInsets.only(top: 4, bottom: 4),
                            child: Text(
                              _freeView
                                  ? 'ドラッグで視点を操作 / 前後ボタンで切り替え'
                                  : compact
                                  ? '上下にスワイプして切り替え'
                                  : '上下にスクロール / ↑ ↓ キーで切り替え',
                              style: Theme.of(context).textTheme.labelSmall
                                  ?.copyWith(
                                    color: Theme.of(context)
                                        .colorScheme
                                        .onSurfaceVariant,
                                  ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                  if (wide) ...[
                    const SizedBox(width: 40),
                    SizedBox(
                      width: 240,
                      child: _ClipList(
                        clips: widget.clips,
                        selected: _selected,
                        onSelect: _select,
                      ),
                    ),
                  ],
                ],
              ),
            );
          },
        ),
      ),
    ),
  );
}

class _Introduction extends StatelessWidget {
  const _Introduction({required this.hasFreeView});
  final bool hasFreeView;
  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 32),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Icon(
          Icons.replay,
          color: Theme.of(context).colorScheme.primary,
          size: 32,
        ),
        const SizedBox(height: 24),
        Text(
          'スポーツを、\nもう一度。',
          style: Theme.of(context).textTheme.headlineLarge
              ?.copyWith(height: 1.4, fontWeight: FontWeight.w700),
        ),
        const SizedBox(height: 20),
        Text(
          '決定的な瞬間を、\n自分の視点で見返す。',
          style: Theme.of(context).textTheme.bodyLarge?.copyWith(
            height: 1.8,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
        const SizedBox(height: 32),
        const Divider(),
        const SizedBox(height: 16),
        Text(
          hasFreeView
              ? 'サンプルの時刻を選んで、\n視点を動かして見返せます。'
              : '短い再構成サンプルを\n固定視点で確認できます。',
          style: Theme.of(context).textTheme.bodySmall?.copyWith(
            height: 1.8,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
        ),
      ],
    ),
  );
}

class _ClipList extends StatelessWidget {
  const _ClipList({
    required this.clips,
    required this.selected,
    required this.onSelect,
  });
  final List<Clip> clips;
  final int selected;
  final ValueChanged<int> onSelect;

  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
        child: Text('デモカタログ', style: Theme.of(context).textTheme.titleSmall),
      ),
      Expanded(
        child: ListView.separated(
          itemCount: clips.length,
          separatorBuilder: (context, index) => const SizedBox(height: 4),
          itemBuilder: (context, index) => ListTile(
            selected: selected == index,
            selectedTileColor: Theme.of(context)
                .colorScheme
                .surfaceContainerHigh,
            selectedColor: Theme.of(context).colorScheme.primary,
            shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(12),
            ),
            leading: Text('${index + 1}'.padLeft(2, '0')),
            title: Text(
              clips[index].title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
            subtitle: Text(clips[index].previewUrl == null ? '映像準備中' : '通常動画'),
            onTap: () => onSelect(index),
          ),
        ),
      ),
    ],
  );
}
