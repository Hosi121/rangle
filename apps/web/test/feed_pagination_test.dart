import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/widgets/clip_card.dart';

import 'clip_paging_controller_test.dart' show Pages, page;
import 'support/fake_viewer.dart';

Finder navButton(String tooltip) => find.byWidgetPredicate(
  (widget) => widget is IconButton && widget.tooltip == tooltip,
);
Finder nextButton() => navButton('次のハイライト');

Future<void> moveTo(WidgetTester tester, int index) async {
  final view = tester.widget<PageView>(find.byKey(const Key('highlight-feed')));
  view.controller!.jumpToPage(index);
  await tester.pumpAndSettle();
}

Future<void> showFeed(
  WidgetTester tester,
  Pages repo, {
  FakeViewer Function(Uri)? video,
  FakeViewer Function(Uri)? scene,
  String? initialClipId,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    RangleApp(
      repository: repo,
      viewerFactory: video ?? (_) => FakeViewer(),
      sceneFactory: scene ?? (_) => FakeViewer(surfaceKey: 'scene'),
      initialClipId: initialClipId,
    ),
  );
  await tester.pumpAndSettle();
}

double? feedPage(WidgetTester tester) => tester
    .widget<PageView>(find.byKey(const Key('highlight-feed')))
    .controller!
    .page;

void main() {
  testWidgets(
    'pending append preserves selection, free view, time and controller',
    (tester) async {
      final pending = Completer<ClipPage>();
      final clips = List.generate(
        20,
        (id) => Clip(
          id: '$id',
          title: 'Clip $id',
          sport: 'sport',
          description: '',
          previewUrl: Uri.parse('https://rangle.test/$id.mp4'),
          sceneUrl: Uri.parse('https://rangle.test/$id.json'),
        ),
      );
      final repo = Pages(
        (cursor, _, _, _) async =>
            cursor == null ? ClipPage(clips, nextCursor: '20') : pending.future,
      );
      final videos = <FakeViewer>[], scenes = <FakeViewer>[];
      await showFeed(
        tester,
        repo,
        video: (_) {
          final value = FakeViewer();
          videos.add(value);
          return value;
        },
        scene: (_) {
          final value = FakeViewer(surfaceKey: 'scene');
          scenes.add(value);
          return value;
        },
      );
      expect(
        videos.length,
        1,
      ); // Metadata pages do not instantiate every asset.
      await moveTo(tester, 19);
      for (var i = 0; i < 15; i++) {
        await tester.tap(nextButton());
        await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      }
      await tester.drag(
        find.byKey(const Key('highlight-feed')),
        const Offset(0, -500),
      );
      await tester.pumpAndSettle();
      expect(repo.calls, [null, '20']);
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      final scene = scenes.single;
      await scene.seek(const Duration(seconds: 7));
      final videoCount = videos.length;
      pending.complete(page(20, 20));
      await tester.pumpAndSettle();
      expect(feedPage(tester), 19);
      expect(find.text('Clip 19'), findsOneWidget);
      expect(find.text('動画に戻る'), findsOneWidget);
      expect(scenes.single, same(scene));
      expect(scene.disposed, isFalse);
      expect(scene.value.position, const Duration(seconds: 7));
      expect(videos.length, videoCount);
      expect(repo.calls.length, 2);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('failed continuation retains cards and retries that page only', (
    tester,
  ) async {
    var attempts = 0;
    final repo = Pages((cursor, _, _, _) async {
      if (cursor == null) return page(0, 20, next: '20');
      if (++attempts == 1) throw StateError('private diagnostic');
      return page(20, 5);
    });
    await showFeed(tester, repo);
    await moveTo(tester, 19);
    expect(find.text('続きを再試行'), findsOneWidget);
    expect(find.textContaining('private diagnostic'), findsNothing);
    for (var i = 0; i < 10; i++) {
      await tester.tap(nextButton());
    }
    expect(repo.calls, [null, '20']);
    await tester.tap(find.text('続きを再試行'));
    await tester.pumpAndSettle();
    expect(repo.calls, [null, '20', '20']);
    expect(feedPage(tester), 19);
    await moveTo(tester, 24);
    expect(tester.widget<IconButton>(nextButton()).onPressed, isNull);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'eviction keeps absolute selection and clamps back gestures to retained cards',
    (tester) async {
      final pending = Completer<ClipPage>();
      final scene = FakeViewer(surfaceKey: 'retained-scene');
      final repo = Pages((cursor, _, _, _) async {
        final start = int.parse(cursor ?? '0');
        if (start == 120) return pending.future;
        return ClipPage(
          List.generate(20, (offset) {
            final id = start + offset;
            return Clip(
              id: '$id',
              title: 'Clip $id',
              sport: 'sport',
              description: '',
              sceneUrl: id == 119
                  ? Uri.parse('https://rangle.test/119.json')
                  : null,
            );
          }),
          nextCursor: '${start + 20}',
        );
      });
      await showFeed(tester, repo, scene: (_) => scene);
      for (final index in [19, 39, 59, 79, 99, 119]) {
        await moveTo(tester, index);
      }
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      await scene.seek(const Duration(seconds: 9));
      pending.complete(page(120, 20));
      await tester.pumpAndSettle();
      expect(scene.disposed, isFalse);
      expect(scene.value.position, const Duration(seconds: 9));
      expect(find.byKey(const Key('retained-scene')), findsOneWidget);
      expect(find.text('動画に戻る'), findsOneWidget);
      expect(feedPage(tester), 119);
      expect(find.text('Clip 119'), findsOneWidget);
      await tester.tap(find.text('動画に戻る'));
      await tester.pumpAndSettle();
      expect(repo.calls.length, 7);
      await moveTo(tester, 40);
      expect(find.text('Clip 40'), findsOneWidget);
      expect(tester.widget<IconButton>(navButton('前のハイライト')).onPressed, isNull);
      await tester.drag(
        find.byKey(const Key('highlight-feed')),
        const Offset(0, 600),
      );
      await tester.pumpAndSettle();
      expect(find.text('Clip 40'), findsOneWidget);
      expect(
        tester
            .widget<PageView>(find.byKey(const Key('highlight-feed')))
            .controller!
            .page,
        40,
      );
      await moveTo(tester, 139);
      expect(feedPage(tester), 139);
      expect(tester.widget<IconButton>(nextButton()).onPressed, isNull);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('query change cancels and discards an old pending page', (
    tester,
  ) async {
    final pending = Completer<ClipPage>();
    var aborted = false;
    final repo = Pages((cursor, query, _, abort) {
      if (query.isNotEmpty) return Future.value(page(500, 1));
      if (cursor == null) return Future.value(page(0, 20, next: '20'));
      unawaited(abort!.then((_) => aborted = true));
      return pending.future;
    });
    await showFeed(tester, repo);
    await moveTo(tester, 19);
    await tester.tap(find.byTooltip('検索'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField), 'new');
    await tester.testTextInput.receiveAction(TextInputAction.search);
    await tester.pumpAndSettle();
    pending.complete(page(20, 20));
    await tester.pumpAndSettle();
    expect(aborted, isTrue);
    expect(find.text('Clip 500'), findsOneWidget);
    expect(find.byKey(const Key('highlight-feed')), findsNothing);
    expect(find.byType(ClipCard), findsNothing);
    expect(repo.queries, ['/', '/', 'new/']);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'generated shared clip still opens free view first and survives append',
    (tester) async {
      const id = 'generated-0123456789abcdef0123456789abcdef';
      final pending = Completer<ClipPage>();
      final repo = Pages(
        (cursor, _, _, _) async => cursor == null
            ? ClipPage([
                Clip(
                  id: id,
                  title: 'Generated',
                  sport: 'sport',
                  description: '',
                  sceneUrl: Uri.parse('https://rangle.test/scene.json'),
                ),
              ], nextCursor: 'next')
            : pending.future,
      );
      final scene = FakeViewer(surfaceKey: 'generated-scene');
      await showFeed(tester, repo, initialClipId: id, scene: (_) => scene);
      expect(find.byKey(const Key('generated-scene')), findsOneWidget);
      await scene.seek(const Duration(seconds: 3));
      expect(tester.widget<IconButton>(nextButton()).onPressed, isNull);
      await tester.tap(find.text('続きを読み込む'));
      await tester.pump();
      pending.complete(page(1, 20));
      await tester.pumpAndSettle();
      expect(find.text('動画に戻る'), findsOneWidget);
      expect(scene.disposed, isFalse);
      expect(scene.value.position, const Duration(seconds: 3));
      expect(feedPage(tester), 0);
      expect(tester.takeException(), isNull);
    },
  );
}
