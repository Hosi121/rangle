import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/services.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import 'data/clip_repository.dart';
import 'data/clip_paging_controller.dart';
import 'community/community_api.dart';
import 'community/account_controller.dart';
import 'community/account_dialog.dart';
import 'generation/generation_api.dart';
import 'generation/generation_screen.dart';
import 'generation/open_result.dart';
import 'publishers/publisher_api.dart';
import 'publishers/publisher_home.dart';
import 'publishers/operations_publishers_screen.dart';
import 'publishers/public_publisher_screen.dart';
import 'sharing/share_clip.dart';
import 'live/live_api.dart';
import 'widgets/search_dialog.dart';
import 'widgets/search_results_screen.dart';
import 'models/clip.dart';
import 'viewer/viewer_adapter.dart';
import 'widgets/clip_card.dart';
import 'widgets/brand_art.dart';
import 'widgets/preview_viewer.dart';
import 'widgets/viewer_viewport.dart';

export 'viewer/viewer_adapter.dart';

class ClipFeedScreen extends StatefulWidget {
  const ClipFeedScreen({
    required this.repository,
    required this.viewerFactory,
    required this.sceneFactory,
    this.community,
    this.generation,
    this.initialGeneration = false,
    this.initialPublisher = false,
    this.initialPublicPublisherId,
    this.publisher,
    this.initialClipId,
    this.live,
    this.liveFactory,
    this.initialLiveId,
    this.shareAction = shareClip,
    super.key,
  });
  final ClipRepository repository;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;
  final CommunityApi? community;
  final GenerationApi? generation;
  final bool initialGeneration;
  final bool initialPublisher;
  final String? initialPublicPublisherId;
  final PublisherApi? publisher;
  final String? initialClipId;
  final LiveApi? live;
  final ViewerFactory? liveFactory;
  final String? initialLiveId;
  final ClipShareAction shareAction;

  @override
  State<ClipFeedScreen> createState() => _ClipFeedScreenState();
}

class _ClipFeedScreenState extends State<ClipFeedScreen> {
  late ClipPagingController _paging;
  String? _clipId;
  AccountController? _account;
  Future<void>? _accountReady;
  String _query = '', _tag = '';
  String _lastSearch = '';
  bool _searchOpen = false, _fromSearch = false;
  bool _generationOpen = false, _clipOpening = false;
  bool _publisherPageOpen = false;

  @override
  void initState() {
    super.initState();
    _clipId = widget.initialClipId;
    _paging = ClipPagingController(widget.repository);
    unawaited(_paging.reset(clipId: _clipId));
    if (widget.community != null) {
      _account = AccountController(widget.community!);
      _account!.addListener(_accountChanged);
      _accountReady = _account!.initialize();
      unawaited(_accountReady!);
    }
    if (widget.initialLiveId != null ||
        widget.initialGeneration ||
        widget.initialPublisher ||
        widget.initialPublicPublisherId != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        unawaited(_openRequestedPage());
      });
    }
  }

  void _accountChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _openRequestedPage() async {
    await _accountReady;
    if (!mounted) return;
    if (widget.initialPublicPublisherId case final id?) {
      await _viewPublisher(id);
      return;
    }
    if (widget.initialPublisher) {
      await _openGeneration(publisher: true);
      return;
    }
    if (_account?.user?.isAdmin == true) return;
    if (widget.initialLiveId != null) {
      await _openLive(widget.initialLiveId);
    } else {
      await _openGeneration();
    }
  }

  Future<void> _openLive([String? id]) =>
      _openGeneration(liveViewer: true, liveId: id);

  Future<void> _viewPublisher(String id) async {
    final account = _account;
    if (account == null || _publisherPageOpen || _generationOpen) return;
    setState(() => _publisherPageOpen = true);
    final clip = await Navigator.of(context).push<String>(
      MaterialPageRoute(
        builder: (_) => PublicPublisherScreen(id: id, account: account),
      ),
    );
    if (!mounted) return;
    setState(() => _publisherPageOpen = false);
    if (clip != null) await _openClip(clip);
  }

  Future<void> _openGeneration({
    bool liveViewer = false,
    String? liveId,
    bool publisher = false,
    bool operations = false,
  }) async {
    if (_generationOpen) return;
    var publicationChanged = false;
    setState(() => _generationOpen = true);
    final result = await Navigator.of(context).push<Uri>(
      MaterialPageRoute(
        builder: (context) => operations && _account != null
            ? OperationsPublishersScreen(account: _account!)
            : publisher
            ? PublisherHome(
                live: widget.live,
                liveFactory: widget.liveFactory,
                api:
                    widget.publisher ??
                    PublisherApi(
                      endpoint:
                          widget.community?.endpoint ??
                          Uri.base.resolve('/api/v1/'),
                    ),
                onPublicationChanged: () => publicationChanged = true,
                onOpenResult: (uri) {
                  if (!openGeneratedViewer(uri)) Navigator.pop(context, uri);
                },
              )
            : GenerationScreen(
                account: _account,
                api: widget.generation,
                live: widget.live,
                liveFactory: widget.liveFactory,
                liveViewer: liveViewer,
                initialLiveId: liveId,
                onPublicationChanged: () => publicationChanged = true,
                onOpenResult: (uri) {
                  if (!openGeneratedViewer(uri)) Navigator.pop(context, uri);
                },
              ),
      ),
    );
    if (!mounted) return;
    if (publicationChanged && widget.repository is RefreshableClipRepository) {
      (widget.repository as RefreshableClipRepository).invalidateCache();
    }
    setState(() {
      _generationOpen = false;
      if (result != null) {
        _query = _tag = '';
        _clipId = result.queryParameters['clip'];
        unawaited(_paging.reset(clipId: _clipId));
      } else if (publicationChanged) {
        _reload();
      }
    });
  }

  @override
  void dispose() {
    _paging.dispose();
    _account?.removeListener(_accountChanged);
    _account?.dispose();
    super.dispose();
  }

  @override
  void didUpdateWidget(ClipFeedScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.repository != widget.repository) {
      _paging.dispose();
      _paging = ClipPagingController(widget.repository);
      _clipId = widget.initialClipId;
      _reload();
    } else if (oldWidget.initialClipId != widget.initialClipId) {
      _clipId = widget.initialClipId;
      _query = _tag = '';
      _reload();
    }
  }

  Future<void> _search(Iterable<String> tags) async {
    final value = await showSearchDialog(
      context,
      initial: _lastSearch,
      tags: tags,
    );
    if (value == null || !mounted) return;
    await _showSearchResults(
      query: value.startsWith('#') ? '' : value,
      tag: value.startsWith('#') ? value.substring(1) : '',
    );
  }

  Future<void> _showSearchResults({String query = '', String tag = ''}) async {
    if (_searchOpen) return;
    _paging.cancelPending();
    _lastSearch = tag.isEmpty ? query : '#$tag';
    setState(() => _searchOpen = true);
    final selection = await Navigator.of(context).push<SearchSelection>(
      MaterialPageRoute(
        builder: (_) => SearchResultsScreen(
          repository: widget.repository,
          query: query,
          tag: tag,
          onSearchChanged: (query, tag) =>
              _lastSearch = tag.isEmpty ? query : '#$tag',
        ),
      ),
    );
    if (!mounted) return;
    setState(() => _searchOpen = false);
    if (selection != null) {
      await _openClip(
        selection.clip.id,
        query: selection.query,
        tag: selection.tag,
        fromSearch: true,
      );
    }
  }

  Future<void> _openClip(
    String id, {
    String query = '',
    String tag = '',
    bool fromSearch = false,
  }) async {
    if (_clipOpening) return;
    _clipOpening = true;
    final previous = _paging, generation = _paging.generation;
    final next = ClipPagingController(widget.repository);
    var adopted = false;
    try {
      await next.reset(clipId: id, query: query, tag: tag);
      if (!mounted ||
          _paging != previous ||
          previous.generation != generation) {
        return;
      }
      if (next.error != null ||
          next.clips.isEmpty ||
          next.clips.first.id != id) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('この動画を開けませんでした。時間をおいて再度お試しください。')),
        );
        return;
      }
      setState(() {
        _paging = next;
        _clipId = id;
        _query = query;
        _tag = tag;
        _fromSearch = fromSearch;
      });
      adopted = true;
      previous.dispose();
    } finally {
      if (!adopted) next.dispose();
      _clipOpening = false;
    }
  }

  void _filterQuery(String query) {
    unawaited(_showSearchResults(query: query));
  }

  void _filterTag(String tag) {
    unawaited(_showSearchResults(tag: tag));
  }

  void _clearSearch() {
    setState(() {
      _tag = '';
      _query = '';
      _lastSearch = '';
      _fromSearch = false;
    });
    _reload();
  }

  void _reload() {
    unawaited(
      _paging.reset(
        query: _query,
        tag: _tag,
        clipId: _query.isEmpty && _tag.isEmpty ? _clipId : null,
      ),
    );
  }

  @override
  Widget build(BuildContext context) =>
      _generationOpen || _publisherPageOpen || _searchOpen
      ? const SizedBox.expand()
      : _account?.user?.isAdmin == true && widget.initialClipId == null
      ? GenerationScreen(
          account: _account,
          api: widget.generation,
          live: widget.live,
          liveFactory: widget.liveFactory,
          managementHome: true,
          onManagePublishers: () => _openGeneration(operations: true),
          onPublicationChanged: () {
            if (widget.repository is RefreshableClipRepository) {
              (widget.repository as RefreshableClipRepository)
                  .invalidateCache();
            }
            _reload();
          },
          onOpenResult: openGeneratedViewer,
        )
      : ViewerViewport(
          child: Scaffold(
            backgroundColor: Colors.black,
            floatingActionButton: ListenableBuilder(
              listenable: _paging,
              builder: (context, _) => _paging.clips.isEmpty
                  ? FloatingActionButton.extended(
                      onPressed: _openLive,
                      icon: const Icon(Icons.live_tv),
                      label: const Text('LIVE'),
                    )
                  : const SizedBox.shrink(),
            ),
            body: SafeArea(
              child: ListenableBuilder(
                listenable: _paging,
                builder: (context, _) {
                  if (_paging.clips.isEmpty && _paging.loading) {
                    return const ViewerMessage(
                      icon: Icons.view_agenda_outlined,
                      title: 'ハイライトを読み込んでいます',
                      description: 'しばらくお待ちください。',
                    );
                  }
                  if (_paging.clips.isEmpty && _paging.error != null) {
                    return ViewerMessage(
                      icon: Icons.cloud_off_outlined,
                      title: 'ハイライトを読み込めませんでした',
                      description: '接続状況をご確認のうえ、もう一度お試しください。',
                      action: FilledButton.icon(
                        onPressed: () => _paging.loadNext(retry: true),
                        icon: const Icon(Icons.refresh),
                        label: const Text('再読み込み'),
                      ),
                    );
                  }
                  final clips = _paging.clips;
                  if (clips.isEmpty) {
                    if (_paging.hasMore) {
                      return ViewerMessage(
                        icon: Icons.view_agenda_outlined,
                        title: '続きのハイライトを確認できます',
                        description: '次のページを読み込んでください。',
                        action: OutlinedButton(
                          onPressed: () => _paging.loadNext(),
                          child: const Text('続きを読み込む'),
                        ),
                      );
                    }
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
                    key: ValueKey((_paging, _paging.generation)),
                    paging: _paging,
                    clips: clips,
                    viewerFactory: widget.viewerFactory,
                    sceneFactory: widget.sceneFactory,
                    account: _account,
                    shareAction: widget.shareAction,
                    onOpenPublisher: () => _openGeneration(publisher: true),
                    onViewPublisher: _viewPublisher,
                    onSearch: () =>
                        _search(clips.expand((clip) => clip.tags).toSet()),
                    onTag: _filterTag,
                    onQuery: _filterQuery,
                    onOpenClip: _openClip,
                    onClearSearch: _clearSearch,
                    onShowSearchResults: () =>
                        _showSearchResults(query: _query, tag: _tag),
                    filterLabel: _fromSearch && _query.isEmpty && _tag.isEmpty
                        ? '検索結果'
                        : _tag.isEmpty
                        ? _query
                        : '#$_tag',
                    onLive: _openLive,
                    initialFreeView:
                        RegExp(r'^generated-[a-f0-9]{32}$')
                            .hasMatch(_clipId ?? '') &&
                        _query.isEmpty &&
                        _tag.isEmpty &&
                        clips.first.id == _clipId &&
                        clips.first.freeViewUrl != null,
                  );
                },
              ),
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
    required this.paging,
    required this.viewerFactory,
    required this.sceneFactory,
    required this.account,
    required this.shareAction,
    required this.onSearch,
    required this.onLive,
    required this.onTag,
    required this.onQuery,
    required this.onOpenClip,
    required this.onClearSearch,
    required this.onShowSearchResults,
    required this.onOpenPublisher,
    required this.onViewPublisher,
    required this.filterLabel,
    this.initialFreeView = false,
    super.key,
  });
  final List<Clip> clips;
  final ClipPagingController paging;
  final ViewerFactory viewerFactory;
  final ViewerFactory sceneFactory;
  final AccountController? account;
  final ClipShareAction shareAction;
  final VoidCallback onSearch, onClearSearch, onLive, onOpenPublisher;
  final VoidCallback onShowSearchResults;
  final ValueChanged<String> onTag, onQuery, onOpenClip;
  final ValueChanged<String> onViewPublisher;
  final String filterLabel;
  final bool initialFreeView;

  @override
  State<_HighlightFeed> createState() => _HighlightFeedState();
}

class _HighlightFeedState extends State<_HighlightFeed> {
  late final PageController _pages;
  late int _selected;
  bool _freeView = false;
  Timer? _wheelIdle;
  double _wheelDelta = 0;
  bool _wheelLatched = false;
  bool _pageTransition = false;

  void _onWheel(PointerSignalEvent event) {
    if (event is! PointerScrollEvent ||
        event.scrollDelta.dy.abs() <= event.scrollDelta.dx.abs()) {
      return;
    }
    // This listener lives inside the Scrollable: resolving here prevents its
    // continuous wheel handler from racing page selection or skipping clips.
    GestureBinding.instance.pointerSignalResolver.register(event, (_) {
      _wheelIdle?.cancel();
      _wheelIdle = Timer(const Duration(milliseconds: 240), () {
        _wheelDelta = 0;
        _wheelLatched = false;
      });
      if (_freeView || _wheelLatched) return;
      _wheelDelta += event.scrollDelta.dy;
      if (_wheelDelta.abs() < 24) return;
      _wheelLatched = true;
      _select(_selected + (_wheelDelta > 0 ? 1 : -1));
    });
  }

  @override
  void initState() {
    super.initState();
    _selected = widget.paging.selectedIndex;
    _pages = PageController(initialPage: _selected);
    _freeView = widget.initialFreeView && _selected == 0;
  }

  void _select(int index) {
    if (_freeView || _pageTransition) return;
    if (index >= widget.paging.endIndex) {
      unawaited(widget.paging.loadNext());
      return;
    }
    if (index < widget.paging.firstIndex || index == _selected) return;
    if (MediaQuery.disableAnimationsOf(context)) {
      _pages.jumpToPage(index);
      return;
    }
    _pageTransition = true;
    unawaited(
      _pages
          .animateToPage(
            index,
            duration: const Duration(milliseconds: 380),
            curve: Curves.easeInOutCubic,
          )
          .whenComplete(() {
            _pageTransition = false;
          }),
    );
  }

  @override
  void dispose() {
    _wheelIdle?.cancel();
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
            return Center(
              child: SizedBox(
                key: const Key('feed-player'),
                width: constraints.maxWidth,
                height: constraints.maxHeight,
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    Listener(
                      onPointerSignal: _onWheel,
                      child: PageView.builder(
                        key: const Key('highlight-feed'),
                        controller: _pages,
                        scrollDirection: Axis.vertical,
                        physics: _freeView
                            ? const NeverScrollableScrollPhysics()
                            : _FeedWindowPhysics(
                                widget.paging,
                                transitioning: () => _pageTransition,
                              ),
                        itemCount: widget.paging.endIndex,
                        onPageChanged: (index) {
                          setState(() {
                            _selected = index;
                            _freeView = false;
                          });
                          widget.paging.select(index);
                        },
                        itemBuilder: (context, index) {
                          final offset = index - widget.paging.firstIndex;
                          if (offset < 0) return const SizedBox.expand();
                          final clip = widget.clips[offset];
                          return ExcludeSemantics(
                            key: ValueKey(clip.id),
                            excluding: index != _selected,
                            child: Listener(
                              onPointerSignal: _onWheel,
                              child: ClipCard(
                                clip: clip,
                                active: index == _selected,
                                viewerFactory: widget.viewerFactory,
                                sceneFactory: widget.sceneFactory,
                                account: widget.account,
                                shareAction: widget.shareAction,
                                onTag: widget.onTag,
                                onQuery: widget.onQuery,
                                onViewPublisher: widget.account == null
                                    ? null
                                    : widget.onViewPublisher,
                                onFreeViewChanged: (value) =>
                                    setState(() => _freeView = value),
                                onNext:
                                    index < widget.paging.endIndex - 1 ||
                                        widget.paging.hasMore
                                    ? () => _select(index + 1)
                                    : null,
                                initialFreeView:
                                    widget.initialFreeView && index == 0,
                              ),
                            ),
                          );
                        },
                      ),
                    ),
                    if (_selected >= widget.paging.endIndex - 2 &&
                        (widget.paging.loading ||
                            widget.paging.error != null ||
                            widget.paging.hasMore))
                      Positioned(
                        bottom: 142,
                        left: 16,
                        right: 72,
                        child: PointerInterceptor(
                          intercepting: _freeView,
                          child: Align(
                            alignment: Alignment.bottomLeft,
                            child: widget.paging.loading
                                ? const Text(
                                    '続きを読み込んでいます',
                                    key: Key('feed-page-loading'),
                                  )
                                : TextButton.icon(
                                    key: const Key('feed-page-action'),
                                    onPressed: () => widget.paging.loadNext(
                                      retry: widget.paging.error != null,
                                    ),
                                    icon: Icon(
                                      widget.paging.error != null
                                          ? Icons.refresh
                                          : Icons.arrow_downward,
                                    ),
                                    label: Text(
                                      widget.paging.error != null
                                          ? '続きを再試行'
                                          : '続きを読み込む',
                                    ),
                                  ),
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
                                child: Align(
                                  alignment: Alignment.centerLeft,
                                  child: RangleWordmark(width: 206),
                                ),
                              ),
                            ),
                          ),
                          PointerInterceptor(
                            intercepting: _freeView,
                            child: IconButton(
                              tooltip: 'LIVEを開く',
                              onPressed: widget.onLive,
                              icon: const Icon(Icons.live_tv),
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
                                    onOpenClip: widget.onOpenClip,
                                    onOpenPublisher: widget.onOpenPublisher,
                                    onViewPublisher: widget.onViewPublisher,
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
                                child: PointerInterceptor(
                                  intercepting: _freeView,
                                  child: Tooltip(
                                    message: '検索結果に戻る',
                                    child: TextButton.icon(
                                      onPressed: widget.onShowSearchResults,
                                      icon: const Icon(
                                        Icons.arrow_back,
                                        size: 16,
                                      ),
                                      label: Text(
                                        widget.filterLabel,
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                      ),
                                    ),
                                  ),
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
                              onPressed:
                                  !_freeView &&
                                      _selected > widget.paging.firstIndex
                                  ? () => _select(_selected - 1)
                                  : null,
                              icon: const Icon(Icons.keyboard_arrow_up),
                            ),
                            const SizedBox(height: 8),
                            IconButton.filledTonal(
                              tooltip: '次のハイライト',
                              onPressed:
                                  !_freeView &&
                                      (_selected < widget.paging.endIndex - 1 ||
                                          widget.paging.hasMore)
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

/// Clamp backward gestures at the retained window without renumbering pages.
class _FeedWindowPhysics extends ScrollPhysics {
  const _FeedWindowPhysics(
    this.paging, {
    required this.transitioning,
    super.parent,
  });
  final ClipPagingController paging;
  final bool Function() transitioning;

  // Animated scrolling temporarily ignores its children. Let the outer wheel
  // listener consume momentum instead of cancelling the slide mid-frame.
  @override
  bool shouldAcceptUserOffset(ScrollMetrics position) =>
      !transitioning() && super.shouldAcceptUserOffset(position);

  @override
  _FeedWindowPhysics applyTo(ScrollPhysics? ancestor) => _FeedWindowPhysics(
    paging,
    transitioning: transitioning,
    parent: buildParent(ancestor),
  );

  @override
  double applyBoundaryConditions(ScrollMetrics position, double value) {
    final floor = paging.firstIndex * position.viewportDimension;
    if (value < position.pixels && position.pixels <= floor) {
      return value - position.pixels;
    }
    if (value < floor && position.pixels > floor) return value - floor;
    return super.applyBoundaryConditions(position, value);
  }
}
