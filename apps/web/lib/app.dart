import 'dart:math' as math;
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import 'data/clip_repository.dart';
import 'community/community_api.dart';
import 'community/account_controller.dart';
import 'community/account_dialog.dart';
import 'generation/generation_api.dart';
import 'generation/generation_screen.dart';
import 'generation/open_result.dart';
import 'sharing/share_clip.dart';
import 'widgets/search_dialog.dart';
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
    this.community,
    this.generation,
    this.initialGeneration = false,
    this.initialClipId,
    this.shareAction = shareClip,
    super.key,
  });
  final ClipRepository repository;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;
  final CommunityApi? community;
  final GenerationApi? generation;
  final bool initialGeneration;
  final String? initialClipId;
  final ClipShareAction shareAction;

  @override
  State<ClipFeedScreen> createState() => _ClipFeedScreenState();
}

class _ClipFeedScreenState extends State<ClipFeedScreen> {
  late Future<List<Clip>> _clips;
  AccountController? _account;
  String _query = '', _tag = '';
  bool _generationOpen = false;

  @override
  void initState() {
    super.initState();
    _clips = widget.repository.load(clipId: widget.initialClipId);
    if (widget.community != null) {
      _account = AccountController(widget.community!);
      unawaited(_account!.initialize());
    }
    if (widget.initialGeneration) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) unawaited(_openGeneration());
      });
    }
  }

  Future<void> _openGeneration() async {
    if (_generationOpen) return;
    setState(() => _generationOpen = true);
    final result = await Navigator.of(context).push<Uri>(
      MaterialPageRoute(
        builder: (context) => GenerationScreen(
          account: _account,
          api: widget.generation,
          onOpenResult: (uri) {
            if (!openGeneratedViewer(uri)) Navigator.pop(context, uri);
          },
        ),
      ),
    );
    if (!mounted) return;
    setState(() {
      _generationOpen = false;
      if (result != null) {
        _query = _tag = '';
        _clips = widget.repository.load(clipId: result.queryParameters['clip']);
      }
    });
  }

  @override
  void dispose() {
    _account?.dispose();
    super.dispose();
  }

  Future<void> _search(Iterable<String> tags) async {
    final value = await showSearchDialog(
      context,
      initial: _tag.isEmpty ? _query : '#$_tag',
      tags: tags,
    );
    if (value == null || !mounted) return;
    setState(() {
      _tag = value.startsWith('#') ? value.substring(1) : '';
      _query = value.startsWith('#') ? '' : value;
    });
    _reload();
  }

  void _filterTag(String tag) {
    setState(() {
      _tag = tag;
      _query = '';
    });
    _reload();
  }

  void _clearSearch() {
    setState(() {
      _tag = '';
      _query = '';
    });
    _reload();
  }

  void _reload() {
    final clips = widget.repository.load(
      query: _query,
      tag: _tag,
      clipId: _query.isEmpty && _tag.isEmpty ? widget.initialClipId : null,
    );
    setState(() {
      _clips = clips;
    });
  }

  @override
  Widget build(BuildContext context) => _generationOpen
      ? const SizedBox.expand()
      : Scaffold(
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
                  if (_query.isNotEmpty || _tag.isNotEmpty) {
                    return ViewerMessage(
                      icon: Icons.search_off,
                      title: 'ハイライトが見つかりませんでした',
                      description: '別のキーワードやハッシュタグで検索できます。',
                      action: OutlinedButton(
                        onPressed: _clearSearch,
                        child: const Text('すべてのハイライトを見る'),
                      ),
                    );
                  }
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
                  account: _account,
                  shareAction: widget.shareAction,
                  onSearch: () =>
                      _search(clips.expand((clip) => clip.tags).toSet()),
                  onTag: _filterTag,
                  onClearSearch: _clearSearch,
                  filterLabel: _tag.isEmpty ? _query : '#$_tag',
                  onGenerate: _openGeneration,
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
    required this.account,
    required this.shareAction,
    required this.onSearch,
    required this.onGenerate,
    required this.onTag,
    required this.onClearSearch,
    required this.filterLabel,
    super.key,
  });
  final List<Clip> clips;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;
  final AccountController? account;
  final ClipShareAction shareAction;
  final VoidCallback onSearch, onClearSearch, onGenerate;
  final ValueChanged<String> onTag;
  final String filterLabel;

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
                          account: widget.account,
                          shareAction: widget.shareAction,
                          onTag: widget.onTag,
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
                                  style: Theme.of(context).textTheme.titleMedium
                                      ?.copyWith(fontWeight: FontWeight.w800),
                                ),
                              ),
                            ),
                          ),
                          Semantics(
                            liveRegion: true,
                            label: '${widget.clips.length}件中${_selected + 1}件目',
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
                          PointerInterceptor(
                            intercepting: _freeView,
                            child: IconButton(
                              tooltip: '3DGSを生成',
                              onPressed: widget.onGenerate,
                              icon: const Icon(Icons.add_box_outlined),
                            ),
                          ),
                          PointerInterceptor(
                            intercepting: _freeView,
                            child: IconButton(
                              tooltip: '検索',
                              onPressed: widget.onSearch,
                              icon: const Icon(Icons.search),
                            ),
                          ),
                          if (widget.account != null)
                            PointerInterceptor(
                              intercepting: _freeView,
                              child: ListenableBuilder(
                                listenable: widget.account!,
                                builder: (context, _) => IconButton(
                                  tooltip: widget.account!.user == null
                                      ? 'ログイン'
                                      : 'アカウント',
                                  onPressed: () => showAccountDialog(
                                    context,
                                    widget.account,
                                  ),
                                  icon: Icon(
                                    widget.account!.user == null
                                        ? Icons.person_outline
                                        : Icons.account_circle,
                                  ),
                                ),
                              ),
                            ),
                        ],
                      ),
                    ),
                    if (widget.filterLabel.isNotEmpty)
                      Positioned(
                        top: 68,
                        left: 16,
                        right: 72,
                        child: DecoratedBox(
                          decoration: BoxDecoration(
                            color: Colors.black.withValues(alpha: .7),
                            borderRadius: BorderRadius.circular(20),
                          ),
                          child: Row(
                            children: [
                              const SizedBox(width: 12),
                              Expanded(
                                child: Text(
                                  widget.filterLabel,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                              PointerInterceptor(
                                intercepting: _freeView,
                                child: IconButton(
                                  tooltip: '絞り込み解除',
                                  onPressed: widget.onClearSearch,
                                  icon: const Icon(Icons.close, size: 18),
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    Positioned(
                      right: 8,
                      top: constraints.maxHeight < 500
                          ? constraints.maxHeight * .4
                          : widget.filterLabel.isEmpty
                          ? 76
                          : 132,
                      child: PointerInterceptor(
                        intercepting: _freeView,
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
