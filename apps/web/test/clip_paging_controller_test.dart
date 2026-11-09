import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_paging_controller.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/models/clip.dart';

Clip row(int id) =>
    Clip(id: '$id', title: 'Clip $id', sport: 'sport', description: '');
ClipPage page(int start, int count, {String? next}) => ClipPage(
  List.generate(count, (index) => row(start + index)),
  nextCursor: next,
);

typedef PageLoader = Future<ClipPage> Function(
  String?,
  String,
  String?,
  Future<void>?,
);

class Pages implements PagedClipRepository {
  Pages(this.fetch);
  final PageLoader fetch;
  final calls = <String?>[];
  final queries = <String>[];
  final details = <String?>[];
  @override
  Future<ClipPage> loadPage({
    String query = '',
    String tag = '',
    String? clipId,
    String? cursor,
    int limit = 20,
    Future<void>? abortTrigger,
  }) {
    calls.add(cursor);
    queries.add('$query/$tag');
    details.add(clipId);
    return fetch(cursor, query, clipId, abortTrigger);
  }

  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async => (await loadPage(query: query, tag: tag, clipId: clipId)).clips;
}

void main() {
  test(
    '55 rows traverse 20/20/15 pages and stop only without next cursor',
    () async {
      final repo = Pages(
        (cursor, _, _, _) async => switch (cursor) {
          null => page(0, 20, next: 'p20'),
          'p20' => page(20, 20, next: 'p40'),
          _ => page(40, 15),
        },
      );
      final feed = ClipPagingController(repo);
      addTearDown(feed.dispose);
      await feed.reset(clipId: 'shared');
      expect(feed.hasMore, isTrue);
      await feed.loadNext();
      await feed.loadNext();
      await feed.loadNext();
      expect(
        feed.clips.map((clip) => clip.id),
        List.generate(55, (id) => '$id'),
      );
      expect(repo.calls, [null, 'p20', 'p40']);
      expect(repo.details, ['shared', null, null]);
      expect(feed.hasMore, isFalse);
    },
  );

  test('navigation bursts single-flight; only the failed cursor is manually retried', () async {
    final pending = Completer<ClipPage>();
    var tries = 0;
    final repo = Pages((cursor, _, _, _) async {
      if (cursor == null) {
        return ClipPage([row(0), row(0), row(1)], nextCursor: 'next');
      }
      if (++tries == 1) return pending.future;
      return ClipPage([row(1), row(2), row(2)]);
    });
    final feed = ClipPagingController(repo);
    addTearDown(feed.dispose);
    await feed.reset();
    final first = feed.loadNext();
    for (var i = 0; i < 30; i++) {
      expect(identical(feed.loadNext(), first), isTrue);
    }
    pending.completeError(StateError('offline'));
    await first;
    expect(feed.clips.map((clip) => clip.id), ['0', '1']);
    expect(feed.error, isNotNull);
    for (var i = 0; i < 30; i++) {
      await feed.loadNext();
    }
    expect(repo.calls, [null, 'next']);
    await feed.loadNext(retry: true);
    expect(repo.calls, [null, 'next', 'next']);
    expect(feed.clips.map((clip) => clip.id), ['0', '1', '2']);
    expect(feed.error, isNull);
  });

  test(
    'stale cursor restarts once; repeated rejection waits for manual action',
    () async {
      final repo = Pages((cursor, _, _, _) async {
        if (cursor != null) throw const StaleClipCursor();
        return page(0, 20, next: 'stale');
      });
      final feed = ClipPagingController(repo);
      addTearDown(feed.dispose);
      await feed.reset(query: 'goal', tag: 'sport');
      await feed.loadNext();
      expect(repo.calls, [null, 'stale', null]);
      expect(feed.generation, 2);
      expect(feed.clips.length, 20);
      await feed.loadNext();
      for (var i = 0; i < 10; i++) {
        await feed.loadNext();
      }
      expect(repo.calls, [null, 'stale', null, 'stale']);
      expect(feed.error, isA<StaleClipCursor>());
      await feed.loadNext(retry: true);
      expect(repo.calls.last, 'stale');
      expect(repo.calls.where((cursor) => cursor == null).length, 2);
      expect(repo.queries.every((value) => value == 'goal/sport'), isTrue);
    },
  );

  test(
    'first-page rejection does not loop and a stuck cursor fails visibly',
    () async {
      final failed = Pages((_, _, _, _) async => throw const StaleClipCursor());
      final feed = ClipPagingController(failed);
      addTearDown(feed.dispose);
      await feed.reset();
      expect(failed.calls.length, 1);
      expect(feed.error, isA<StaleClipCursor>());
      final stuck = ClipPagingController(
        Pages((cursor, _, _, _) async => page(0, 1, next: 'same')),
      );
      addTearDown(stuck.dispose);
      await stuck.reset();
      await stuck.loadNext();
      expect(stuck.error, isFormatException);
      expect(stuck.hasMore, isTrue);
    },
  );

  test(
    'retained metadata and dedupe window stay bounded while reaching row 240',
    () async {
      final repo = Pages((cursor, _, _, _) async {
        final start = int.parse(cursor ?? '0');
        return page(start, 20, next: start < 220 ? '${start + 20}' : null);
      });
      final feed = ClipPagingController(repo);
      addTearDown(feed.dispose);
      await feed.reset();
      while (feed.hasMore) {
        final selected = feed.endIndex - 1;
        final clip = feed.clips.last;
        feed.select(selected);
        await feed.loadNext();
        expect(feed.clips.length, lessThanOrEqualTo(100));
        expect(feed.selectedIndex, selected);
        expect(feed.clips[selected - feed.firstIndex], same(clip));
      }
      expect(feed.firstIndex, 140);
      expect(feed.endIndex, 240);
      expect(feed.clips.last.id, '239');
      expect(repo.calls.length, 12);
    },
  );

  test(
    'capacity pauses fetching without claiming end and never evicts selection',
    () async {
      final repo = Pages(
        (cursor, _, _, _) async => page(
          cursor == null ? 0 : 20,
          20,
          next: cursor == null ? 'more' : 'last',
        ),
      );
      final feed = ClipPagingController(repo, maxClips: 21);
      addTearDown(feed.dispose);
      await feed.reset();
      await feed.loadNext();
      expect(feed.hasMore, isTrue);
      expect(feed.canLoadMore, isFalse);
      expect(repo.calls.length, 1);
      feed.select(19);
      await feed.loadNext();
      expect(feed.clips.length, 21);
      expect(feed.firstIndex, 19);
      expect(feed.selectedIndex, 19);
      expect(feed.clips.first.id, '19');
    },
  );

  test(
    'shared clip is deduplicated even after its pinned entry is evicted',
    () async {
      final repo = Pages(
        (cursor, _, _, _) async => switch (cursor) {
          null => ClipPage([row(100), row(0), row(1)], nextCursor: 'second'),
          'second' => ClipPage([row(2), row(3)], nextCursor: 'third'),
          _ => ClipPage([row(100), row(4)]),
        },
      );
      final feed = ClipPagingController(repo, pageSize: 2, maxClips: 3);
      addTearDown(feed.dispose);
      await feed.reset(clipId: '100');
      feed.select(2);
      await feed.loadNext();
      expect(feed.clips.map((clip) => clip.id), ['1', '2', '3']);
      feed.select(4);
      await feed.loadNext();
      expect(feed.clips.map((clip) => clip.id), ['2', '3', '4']);
      expect(feed.endIndex, 6);
      expect(feed.hasMore, isFalse);
    },
  );

  test(
    'query/tag/deep-link epochs abort and ignore late success and failure',
    () async {
      final old = Completer<ClipPage>();
      var aborted = false;
      final repo = Pages((_, query, _, abort) {
        if (query == 'old') {
          unawaited(abort!.then((_) => aborted = true));
          return old.future;
        }
        return Future.value(page(99, 1));
      });
      final feed = ClipPagingController(repo);
      addTearDown(feed.dispose);
      final obsolete = feed.reset(
        query: 'old',
        tag: 'old-tag',
        clipId: 'old-id',
      );
      await feed.reset(query: 'new', tag: 'new-tag', clipId: 'new-id');
      old.complete(page(0, 20, next: 'obsolete'));
      await obsolete;
      expect(aborted, isTrue);
      expect(feed.clips.single.id, '99');
      expect(feed.hasMore, isFalse);
      expect(repo.queries, ['old/old-tag', 'new/new-tag']);
      expect(repo.details, ['old-id', 'new-id']);
      final late = Completer<ClipPage>();
      final errors = ClipPagingController(
        Pages(
          (_, query, _, _) =>
              query == 'old' ? late.future : Future.value(page(88, 1)),
        ),
      );
      addTearDown(errors.dispose);
      final ignored = errors.reset(query: 'old');
      await errors.reset(query: 'new');
      late.completeError(StateError('obsolete failure'));
      await ignored;
      expect(errors.error, isNull);
      expect(errors.clips.single.id, '88');
    },
  );
}
