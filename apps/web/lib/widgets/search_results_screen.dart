import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../data/clip_repository.dart';
import '../models/clip.dart';
import '../viewer/thumbnail_source.dart';
import 'rangle_header.dart';
import 'viewer_viewport.dart';

typedef SearchSelection = ({Clip clip, String query, String tag});

class SearchResultsScreen extends StatefulWidget {
  const SearchResultsScreen({
    required this.repository,
    this.query = '',
    this.tag = '',
    this.onSearchChanged,
    this.thumbnailFactory = createThumbnailSource,
    super.key,
  });
  final ClipRepository repository;
  final String query, tag;
  final void Function(String query, String tag)? onSearchChanged;
  final ThumbnailSource Function(Uri) thumbnailFactory;
  @override
  State<SearchResultsScreen> createState() => _SearchResultsScreenState();
}

class _SearchResultsScreenState extends State<SearchResultsScreen> {
  final _form = GlobalKey<FormState>();
  final _focus = FocusNode();
  final _scroll = ScrollController();
  late final _input = TextEditingController(
    text: widget.tag.isEmpty ? widget.query : '#${widget.tag}',
  );
  late final _posters = _PosterQueue(widget.thumbnailFactory);
  final _clips = <Clip>[], _ids = <String>{}, _cursors = <String>{};
  late String _query = widget.query, _tag = widget.tag;
  String? _cursor;
  Object? _error;
  bool _loading = false, _more = true, _restarted = false;
  int _epoch = 0;
  Completer<void>? _abort;

  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  @override
  void dispose() {
    _epoch++;
    _abort?.complete();
    _posters.dispose();
    _input.dispose();
    _focus.dispose();
    _scroll.dispose();
    super.dispose();
  }

  void _submit() {
    if (!_form.currentState!.validate()) return;
    final value = _input.text.trim();
    _query = value.startsWith('#') ? '' : value;
    _tag = value.startsWith('#') ? value.substring(1) : '';
    widget.onSearchChanged?.call(_query, _tag);
    _focus.unfocus();
    _restart();
  }

  void _restart({bool stale = false}) {
    _epoch++;
    _abort?.complete();
    _abort = null;
    _posters.invalidate();
    if (_scroll.hasClients) _scroll.jumpTo(0);
    setState(() {
      _clips.clear();
      _ids.clear();
      _cursors.clear();
      _cursor = null;
      _more = true;
      _loading = false;
      _error = null;
      _restarted = stale;
    });
    unawaited(_load());
  }

  Future<void> _load() async {
    if (_loading || !_more) return;
    final epoch = _epoch, abort = Completer<void>();
    _abort = abort;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final source = widget.repository;
      final page = source is PagedClipRepository
          ? await source.loadPage(
              query: _query,
              tag: _tag,
              cursor: _cursor,
              limit: 20,
              abortTrigger: abort.future,
            )
          : ClipPage(await source.load(query: _query, tag: _tag));
      if (!mounted || epoch != _epoch) return;
      final next = page.nextCursor;
      if (next != null && !_cursors.add(next)) {
        throw const FormatException('Search cursor did not advance.');
      }
      setState(() {
        _clips.addAll(page.clips.where((clip) => _ids.add(clip.id)));
        _cursor = next;
        _more = next != null;
      });
    } catch (e) {
      if (!mounted || epoch != _epoch) return;
      if (e is StaleClipCursor && _cursor != null && !_restarted) {
        _restart(stale: true);
        return;
      }
      setState(() => _error = e);
    } finally {
      if (mounted && epoch == _epoch) {
        setState(() {
          _loading = false;
          _abort = null;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: Scaffold(
      appBar: RangleHeader(title: '検索結果', onBack: () => Navigator.pop(context)),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 16),
              child: Form(
                key: _form,
                child: TextFormField(
                  key: const Key('results-query'),
                  controller: _input,
                  focusNode: _focus,
                  textInputAction: TextInputAction.search,
                  onFieldSubmitted: (_) => _submit(),
                  validator: (value) => utf8.encode(value ?? '').length > 256
                      ? '検索語を短くしてください。'
                      : null,
                  decoration: InputDecoration(
                    labelText: '動画を検索',
                    suffixIcon: IconButton(
                      tooltip: '検索する',
                      onPressed: _submit,
                      icon: const Icon(Icons.search),
                    ),
                  ),
                ),
              ),
            ),
            if (_loading)
              TickerMode(
                enabled: !MediaQuery.disableAnimationsOf(context),
                child: const LinearProgressIndicator(),
              ),
            Expanded(
              child: LayoutBuilder(
                builder: (context, constraints) {
                  final columns = constraints.maxWidth >= 600 ? 3 : 2;
                  final width =
                      (constraints.maxWidth - 32 - 12 * (columns - 1)) /
                      columns;
                  final scale = MediaQuery.textScalerOf(context);
                  final extent =
                      width * 4 / 3 +
                      scale.scale(14) * 2.8 +
                      scale.scale(12) * 1.4 +
                      24;
                  return CustomScrollView(
                    key: const PageStorageKey('search-results'),
                    controller: _scroll,
                    slivers: [
                      SliverPadding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        sliver: SliverGrid(
                          key: ValueKey(_epoch),
                          gridDelegate:
                              SliverGridDelegateWithFixedCrossAxisCount(
                                crossAxisCount: columns,
                                mainAxisExtent: extent,
                                crossAxisSpacing: 12,
                                mainAxisSpacing: 12,
                              ),
                          delegate: SliverChildBuilderDelegate((
                            context,
                            index,
                          ) {
                            final clip = _clips[index];
                            return Material(
                              key: ValueKey('search-result-${clip.id}'),
                              color: Theme.of(context)
                                  .colorScheme
                                  .surfaceContainer,
                              borderRadius: BorderRadius.circular(12),
                              child: InkWell(
                                borderRadius: BorderRadius.circular(12),
                                onTap: () => Navigator.pop(context, (
                                  clip: clip,
                                  query: _query,
                                  tag: _tag,
                                )),
                                child: Column(
                                  crossAxisAlignment:
                                      CrossAxisAlignment.stretch,
                                  children: [
                                    ClipRRect(
                                      borderRadius: BorderRadius.circular(12),
                                      child: AspectRatio(
                                        aspectRatio: 3 / 4,
                                        child: _Poster(
                                          key: ValueKey(clip.previewUrl),
                                          uri: clip.previewUrl,
                                          queue: _posters,
                                        ),
                                      ),
                                    ),
                                    Padding(
                                      padding: const EdgeInsets.fromLTRB(
                                        8,
                                        8,
                                        8,
                                        0,
                                      ),
                                      child: Text(
                                        clip.title,
                                        maxLines: 2,
                                        overflow: TextOverflow.ellipsis,
                                        style: const TextStyle(
                                          fontSize: 14,
                                          height: 1.4,
                                        ),
                                      ),
                                    ),
                                    Padding(
                                      padding: const EdgeInsets.symmetric(
                                        horizontal: 8,
                                      ),
                                      child: Text(
                                        clip.context?.publisher?.name ??
                                            clip.sport,
                                        maxLines: 1,
                                        overflow: TextOverflow.ellipsis,
                                        style: TextStyle(
                                          fontSize: 12,
                                          height: 1.4,
                                          color: Theme.of(context)
                                              .colorScheme
                                              .onSurfaceVariant,
                                        ),
                                      ),
                                    ),
                                  ],
                                ),
                              ),
                            );
                          }, childCount: _clips.length),
                        ),
                      ),
                      SliverToBoxAdapter(
                        child: Padding(
                          padding: const EdgeInsets.all(24),
                          child: Column(
                            children: [
                              if (!_loading &&
                                  _clips.isEmpty &&
                                  _error == null) ...[
                                const Text('動画が見つかりませんでした。'),
                                TextButton(
                                  onPressed: () => _focus.requestFocus(),
                                  child: const Text('検索し直す'),
                                ),
                              ],
                              if (_error != null)
                                Text(
                                  '検索結果を読み込めませんでした。',
                                  style: TextStyle(
                                    color: Theme.of(context).colorScheme.error,
                                  ),
                                ),
                              if (!_loading && (_more || _error != null))
                                TextButton(
                                  onPressed: _error is StaleClipCursor
                                      ? () => _restart()
                                      : _load,
                                  child: Text(
                                    _error == null ? 'もっと見る' : '再読み込み',
                                  ),
                                ),
                            ],
                          ),
                        ),
                      ),
                    ],
                  );
                },
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

/// Only one dedicated video decoder; 48 small JPEGs, and no GS/player instances.
class _PosterQueue {
  _PosterQueue(this.create);
  final ThumbnailSource Function(Uri) create;
  final _cache = <Uri, Uint8List?>{};
  Future<void> _tail = Future.value();
  ThumbnailSource? _active;
  int _epoch = 0;
  bool _disposed = false;
  Future<Uint8List?> load(Uri uri, bool Function() wanted) {
    final result = Completer<Uint8List?>(), epoch = _epoch;
    _tail = _tail.then((_) async {
      if (_disposed || epoch != _epoch || !wanted()) {
        result.complete(null);
        return;
      }
      if (_cache.containsKey(uri)) {
        result.complete(_cache[uri]);
        return;
      }
      ThumbnailSource? source;
      Uint8List? image;
      try {
        source = _active = create(uri);
        image = await source.frame(const Duration(milliseconds: 1));
      } on Object {
        // Unsupported/CORS-blocked previews retain the ordinary video tile.
      } finally {
        source?.dispose();
        _active = null;
      }
      if (!_disposed && epoch == _epoch && wanted()) {
        _cache[uri] = image;
        if (_cache.length > 48) _cache.remove(_cache.keys.first);
        result.complete(image);
      } else {
        result.complete(null);
      }
    });
    return result.future;
  }

  void invalidate() {
    _epoch++;
    _active?.dispose();
    _active = null;
  }

  void dispose() {
    _disposed = true;
    invalidate();
    _cache.clear();
  }
}

class _Poster extends StatefulWidget {
  const _Poster({required this.uri, required this.queue, super.key});
  final Uri? uri;
  final _PosterQueue queue;
  @override
  State<_Poster> createState() => _PosterState();
}

class _PosterState extends State<_Poster> {
  Uint8List? _image;
  int _version = 0;
  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void didUpdateWidget(_Poster oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.uri != widget.uri) {
      _image = null;
      _load();
    }
  }

  void _load() {
    final uri = widget.uri, version = ++_version;
    if (uri == null) return;
    unawaited(
      widget.queue.load(uri, () => mounted && version == _version).then((
        image,
      ) {
        if (mounted && version == _version) setState(() => _image = image);
      }),
    );
  }

  @override
  Widget build(BuildContext context) {
    final fallback = ColoredBox(
      color: Theme.of(context).colorScheme.surfaceContainerHighest,
      child: const Center(child: Icon(Icons.play_circle_outline, size: 36)),
    );
    return _image == null
        ? fallback
        : Image.memory(
            _image!,
            fit: BoxFit.cover,
            excludeFromSemantics: true,
            errorBuilder: (_, _, _) => fallback,
          );
  }
}
