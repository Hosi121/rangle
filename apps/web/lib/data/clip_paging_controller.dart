import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';

import '../models/clip.dart';
import 'clip_repository.dart';

/// A bounded, forward-only catalog window. Absolute indices keep selected cards
/// and media controllers in place while old metadata is evicted.
class ClipPagingController extends ChangeNotifier {
  ClipPagingController(
    this.repository, {
    this.pageSize = 20,
    this.maxClips = 100,
  }) : assert(pageSize > 0 && pageSize <= 50),
       assert(maxClips >= pageSize + 1);

  final ClipRepository repository;
  final int pageSize, maxClips;
  List<Clip> _clips = const [];
  List<Clip> get clips => _clips;
  int firstIndex = 0, selectedIndex = 0, generation = 0;
  int get endIndex => firstIndex + _clips.length;
  bool loading = false;
  Object? error;
  String? _cursor;
  bool _loaded = false, _resetUsed = false, _disposed = false;
  int _epoch = 0;
  String _query = '', _tag = '';
  String? _clipId;
  Completer<void>? _abort;
  Future<void>? _inflight;

  bool get hasMore => !_loaded || _cursor != null;
  bool get canLoadMore =>
      hasMore &&
      (!_loaded ||
          _clips.length +
                  pageSize -
                  math.min(selectedIndex - firstIndex, _clips.length) <=
              maxClips);

  /// Stop hidden prefetch without discarding the current metadata or cursor.
  void cancelPending() {
    if (_disposed || _inflight == null) return;
    _epoch++;
    _abort?.complete();
    _abort = null;
    _inflight = null;
    loading = false;
    notifyListeners();
  }

  Future<void> reset({String query = '', String tag = '', String? clipId}) {
    if (_disposed) return Future.value();
    _epoch++;
    generation++;
    _abort?.complete();
    _abort = null;
    _inflight = null;
    loading = false;
    error = null;
    _loaded = _resetUsed = false;
    _clips = const [];
    _cursor = null;
    firstIndex = selectedIndex = 0;
    _query = query;
    _tag = tag;
    _clipId = clipId;
    return loadNext();
  }

  /// Repeated navigation shares one request. Failed pages require explicit retry.
  Future<void> loadNext({bool retry = false}) {
    if (_disposed) return Future.value();
    if (_inflight != null) return _inflight!;
    if (!canLoadMore || (error != null && !retry)) return Future.value();
    final complete = Completer<void>();
    _inflight = complete.future;
    loading = true;
    error = null;
    final epoch = _epoch;
    final abort = _abort = Completer<void>();
    notifyListeners();
    unawaited(
      _fetch(epoch, abort.future).whenComplete(() {
        if (!_disposed && epoch == _epoch) {
          _inflight = null;
          loading = false;
          _abort = null;
          notifyListeners();
        }
        complete.complete();
      }),
    );
    return complete.future;
  }

  void select(int index) {
    if (index < firstIndex || index >= endIndex) return;
    selectedIndex = index;
    if (index >= endIndex - 2) unawaited(loadNext());
  }

  Future<void> _fetch(int epoch, Future<void> abort) async {
    var cursor = _cursor;
    while (!_disposed && epoch == _epoch) {
      try {
        final source = repository;
        final page = source is PagedClipRepository
            ? await source.loadPage(
                query: _query,
                tag: _tag,
                clipId: cursor == null ? _clipId : null,
                cursor: cursor,
                limit: pageSize,
                abortTrigger: abort,
              )
            : ClipPage(
                await source.load(query: _query, tag: _tag, clipId: _clipId),
              );
        if (_disposed || epoch != _epoch) return;
        if (cursor != null && page.nextCursor == cursor) {
          throw const FormatException('Feed cursor did not advance.');
        }
        final ids = _clips.map((clip) => clip.id).toSet();
        // A shared clip may occur much later in the catalog after its pinned
        // first-page entry has already left the metadata window.
        if (_loaded && _clipId != null) ids.add(_clipId!);
        final incoming = page.clips.where((clip) => ids.add(clip.id)).toList();
        final combined = [..._clips, ...incoming];
        final remove = math.max(0, combined.length - maxClips);
        if (remove > selectedIndex - firstIndex) {
          throw const FormatException('Feed page exceeds the retained window.');
        }
        _clips = List.unmodifiable(combined.skip(remove));
        firstIndex += remove;
        _cursor = page.nextCursor;
        _loaded = true;
        return;
      } catch (failure) {
        if (_disposed || epoch != _epoch) return;
        if (failure is StaleClipCursor && cursor != null && !_resetUsed) {
          // One automatic restart per explicit query/reload generation.
          _resetUsed = true;
          generation++;
          _clips = const [];
          firstIndex = selectedIndex = 0;
          _loaded = false;
          _cursor = cursor = null;
          notifyListeners();
          continue;
        }
        error = failure;
        return;
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _epoch++;
    _abort?.complete();
    _abort = null;
    super.dispose();
  }
}
