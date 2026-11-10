import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/thumbnail_source.dart';
import 'package:rangle_web/widgets/search_results_screen.dart';

import 'support/fake_viewer.dart';

Clip video(String id) => Clip(
  id: id,
  title: 'Video $id',
  sport: 'basketball',
  description: '',
  previewUrl: Uri.parse('https://media.test/$id.webm'),
  sceneUrl: Uri.parse('https://media.test/$id/scene.json'),
);
typedef SearchCall = ({String query, String tag, String? cursor, String? clip});

class SearchRepository implements PagedClipRepository {
  SearchRepository(this.reply);
  final Future<ClipPage> Function(SearchCall) reply;
  final calls = <SearchCall>[];
  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async => (await loadPage(query: query, tag: tag, clipId: clipId)).clips;
  @override
  Future<ClipPage> loadPage({
    String query = '',
    String tag = '',
    String? clipId,
    String? cursor,
    int limit = 20,
    Future<void>? abortTrigger,
  }) {
    final call = (query: query, tag: tag, cursor: cursor, clip: clipId);
    calls.add(call);
    return reply(call);
  }
}

class PendingThumbnail implements ThumbnailSource {
  PendingThumbnail(this.onRelease);
  final void Function() onRelease;
  final pending = Completer<Uint8List?>();
  bool disposed = false;
  @override
  Future<Uint8List?> frame(Duration position) => pending.future;
  @override
  void dispose() {
    if (disposed) return;
    disposed = true;
    onRelease();
    pending.complete(null);
  }
}

void mobile(WidgetTester tester) {
  tester.view.physicalSize = const Size(320, 740);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  testWidgets(
    'search lists videos before playback, selection opens the chosen filtered video, and results can reopen',
    (tester) async {
      mobile(tester);
      final repo = SearchRepository((call) async {
        if (call.query.isEmpty) return ClipPage([video('home')]);
        return ClipPage(
          call.clip == 'second'
              ? [video('second'), video('first')]
              : [video('first'), video('second')],
        );
      });
      final players = <String>[];
      var scenes = 0;
      await tester.pumpWidget(
        RangleApp(
          repository: repo,
          viewerFactory: (clip) {
            players.add(clip.pathSegments.last);
            return FakeViewer();
          },
          sceneFactory: (_) {
            scenes++;
            return FakeViewer();
          },
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('検索'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextFormField), 'goal');
      await tester.testTextInput.receiveAction(TextInputAction.search);
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('highlight-feed')), findsNothing);
      expect(find.text('Video first'), findsOneWidget);
      expect(find.text('Video second'), findsOneWidget);
      expect(players, ['home.webm']);
      expect(scenes, 0);
      await tester.tap(find.byKey(const ValueKey('search-result-second')));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('highlight-feed')), findsOneWidget);
      expect(players.last, 'second.webm');
      expect(repo.calls.last.query, 'goal');
      expect(repo.calls.last.clip, 'second');
      await tester.tap(find.byTooltip('検索結果に戻る'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('results-query')), findsOneWidget);
      expect(find.text('Video first'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets(
    'result refinement ignores an older response and supports the empty state',
    (tester) async {
      final old = Completer<ClipPage>();
      final repo = SearchRepository(
        (call) async => call.query == 'old'
            ? old.future
            : call.query == 'missing'
            ? const ClipPage([])
            : ClipPage([video('new')]),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: SearchResultsScreen(repository: repo, query: 'old'),
        ),
      );
      await tester.pump();
      await tester.enterText(find.byKey(const Key('results-query')), 'new');
      await tester.testTextInput.receiveAction(TextInputAction.search);
      await tester.pumpAndSettle();
      old.complete(ClipPage([video('old')]));
      await tester.pumpAndSettle();
      expect(find.text('Video new'), findsOneWidget);
      expect(find.text('Video old'), findsNothing);
      await tester.enterText(find.byKey(const Key('results-query')), 'missing');
      await tester.testTextInput.receiveAction(TextInputAction.search);
      await tester.pumpAndSettle();
      expect(find.text('動画が見つかりませんでした。'), findsOneWidget);
      expect(find.text('Video new'), findsNothing);
      expect(find.text('検索し直す'), findsOneWidget);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'result pages retry in place and deduplicate videos at increased text size',
    (tester) async {
      mobile(tester);
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      var continuation = 0;
      final repo = SearchRepository((call) async {
        if (call.cursor == null) {
          return ClipPage([video('first')], nextCursor: 'next');
        }
        if (continuation++ == 0) throw Exception('offline');
        return ClipPage([video('first'), video('second')]);
      });
      await tester.pumpWidget(
        MaterialApp(
          home: SearchResultsScreen(repository: repo, tag: 'basketball'),
        ),
      );
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('もっと見る'));
      await tester.tap(find.text('もっと見る'));
      await tester.pumpAndSettle();
      expect(find.text('検索結果を読み込めませんでした。'), findsOneWidget);
      await tester.ensureVisible(find.text('再読み込み'));
      await tester.tap(find.text('再読み込み'));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('search-result-first')), findsOneWidget);
      expect(
        find.byKey(const ValueKey('search-result-second')),
        findsOneWidget,
      );
      expect(repo.calls.every((call) => call.tag == 'basketball'), isTrue);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'thumbnail decoding is serial and cancellation releases the dedicated video source',
    (tester) async {
      final sources = <PendingThumbnail>[];
      var active = 0, maximum = 0;
      final repo = SearchRepository(
        (call) async =>
            ClipPage([video('${call.query}-a'), video('${call.query}-b')]),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: SearchResultsScreen(
            repository: repo,
            query: 'old',
            thumbnailFactory: (_) {
              active++;
              if (active > maximum) maximum = active;
              final source = PendingThumbnail(() => active--);
              sources.add(source);
              return source;
            },
          ),
        ),
      );
      await tester.pump();
      await tester.pump();
      expect(sources, hasLength(1));
      await tester.enterText(find.byKey(const Key('results-query')), 'new');
      await tester.testTextInput.receiveAction(TextInputAction.search);
      await tester.pump();
      await tester.pump();
      expect(sources.first.disposed, isTrue);
      expect(maximum, 1);
      await tester.pumpWidget(const SizedBox());
      await tester.pumpAndSettle();
      expect(active, 0);
      expect(sources.every((source) => source.disposed), isTrue);
      expect(tester.takeException(), isNull);
    },
  );
}
