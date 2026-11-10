import 'dart:convert';
import 'dart:ui' show PointerDeviceKind;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';
import 'package:rangle_web/community/community_api.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/sharing/share_clip.dart';

import 'support/fake_viewer.dart';

class _Clips implements ClipRepository {
  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async => [
    for (final id in ['first', 'second'])
      Clip(
        id: id,
        title: id,
        sport: 'バスケットボール',
        tags: ['自由視点'],
        description: '出典 CC BY 4.0',
        previewUrl: Uri.parse('https://example.invalid/$id.webm'),
        sceneUrl: Uri.parse('https://example.invalid/$id.json'),
      ),
  ];
}

class _Scene extends FakeViewer {
  _Scene() : super(surfaceKey: 'scene-input');

  int drags = 0;

  @override
  Widget buildView() => GestureDetector(
    onPanUpdate: (_) => drags++,
    onDoubleTap: togglePlayback,
    child: const ColoredBox(key: Key('scene-input'), color: Colors.black),
  );
}

final _interceptors = find.byWidgetPredicate(
  (widget) => widget is PointerInterceptor && widget.intercepting,
);

Future<void> _dragScene(
  WidgetTester tester,
  Offset point, {
  PointerDeviceKind kind = PointerDeviceKind.touch,
}) async {
  final gesture = await tester.startGesture(point, kind: kind);
  await gesture.moveBy(const Offset(40, 40));
  await gesture.moveBy(const Offset(20, 20));
  await gesture.up();
  if (kind == PointerDeviceKind.mouse) await gesture.removePointer();
}

void _expectProtected(WidgetTester tester, Finder control) {
  final ancestors = find.ancestor(of: control, matching: _interceptors);
  expect(ancestors, findsWidgets);
  expect(
    ancestors.evaluate().any(
      (element) => tester
          .getRect(find.byWidget(element.widget))
          .contains(tester.getCenter(control)),
    ),
    isTrue,
  );
}

Offset _expectSceneExposed(WidgetTester tester) {
  final surface = find.byKey(const Key('scene-input'));
  expect(find.ancestor(of: surface, matching: _interceptors), findsNothing);
  final player = tester
      .getRect(surface)
      .intersect(tester.getRect(find.byKey(const Key('feed-player'))));
  final point = Offset(player.center.dx, player.top + player.height * .3);
  for (final element in _interceptors.evaluate()) {
    expect(
      tester.getRect(find.byWidget(element.widget)).contains(point),
      isFalse,
    );
  }
  return point;
}

void _expectModalProtected(WidgetTester tester) {
  // Intercept the existing full-screen modal barrier, so both modal content
  // and outside-tap dismissal receive browser input above the iframe.
  final barrier = find.ancestor(
    of: find.byType(ModalBarrier),
    matching: _interceptors,
  );
  expect(barrier, findsOneWidget);
  expect(tester.getRect(barrier), Offset.zero & tester.view.physicalSize);
}

Future<void> _pumpFreeView(
  WidgetTester tester,
  _Scene scene,
  Size size, {
  ClipShareAction? share,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final client = MockClient((request) async {
    final Object data;
    if (request.url.path.endsWith('/auth/me')) {
      data = {'user': null, 'available': true};
    } else if (request.url.path.endsWith('/engagement')) {
      data = {'likes': 0, 'comments': 0, 'liked': false};
    } else if (request.url.path.endsWith('/comments')) {
      data = {'comments': <Object>[], 'next_cursor': ''};
    } else {
      throw StateError('Unexpected request: ${request.url}');
    }
    return http.Response(jsonEncode(data), 200);
  });
  addTearDown(client.close);
  await tester.pumpWidget(
    RangleApp(
      repository: _Clips(),
      community: CommunityApi(
        endpoint: Uri.parse('https://example.invalid/api/v1/'),
        client: client,
      ),
      viewerFactory: (_) => FakeViewer(),
      sceneFactory: (_) => scene,
      shareAction: share ?? (_) async => ShareResult.cancelled,
    ),
  );
  await tester.pumpAndSettle();
  final mode = find.text('自由視点で見る');
  await tester.tap(mode.evaluate().isEmpty ? find.byTooltip('自由視点で見る') : mode);
  await tester.pumpAndSettle();
  expect(find.byKey(const Key('scene-input')), findsOneWidget);
}

void main() {
  // Widget tests cannot reproduce browser iframe event dispatch. These bounds
  // checks prevent losing the DOM interception or covering the orbit surface.
  for (final size in [
    const Size(320, 640),
    const Size(390, 844),
    const Size(844, 390),
    const Size(1440, 900),
  ]) {
    testWidgets(
      'GS overlays at $size protect controls and retain scene input',
      (tester) async {
        final scene = _Scene();
        var shared = 0;
        String? copied;
        final messenger = tester.binding.defaultBinaryMessenger;
        messenger.setMockMethodCallHandler(SystemChannels.platform, (
          call,
        ) async {
          if (call.method == 'Clipboard.setData') {
            copied = (call.arguments as Map)['text'] as String;
          }
          return null;
        });
        addTearDown(
          () =>
              messenger.setMockMethodCallHandler(SystemChannels.platform, null),
        );
        await _pumpFreeView(
          tester,
          scene,
          size,
          share: (_) async {
            shared++;
            return ShareResult.shared;
          },
        );
        for (final tooltip in [
          '共有',
          '声援を送る（何度でも押せます）',
          '概要',
          '検索',
          'ログイン',
          '前のハイライト',
          '次のハイライト',
        ]) {
          _expectProtected(tester, find.byTooltip(tooltip));
        }
        _expectProtected(tester, find.byKey(const Key('media-scrubber')));
        final mode = find.text('動画に戻る').evaluate().isEmpty
            ? find.byTooltip('動画に戻る')
            : find.text('動画に戻る');
        _expectProtected(tester, mode);
        final orbit = _expectSceneExposed(tester);
        await _dragScene(tester, orbit);
        expect(scene.drags, greaterThan(0));
        final touchDrags = scene.drags;
        await _dragScene(tester, orbit, kind: PointerDeviceKind.mouse);
        expect(scene.drags, greaterThan(touchDrags));
        final tags = find.text('#自由視点');
        if (tags.evaluate().isNotEmpty) _expectProtected(tester, tags);
        await tester.tapAt(orbit);
        await tester.pump(const Duration(milliseconds: 50));
        await tester.tapAt(orbit);
        await tester.pumpAndSettle();
        expect(scene.value.playing, isTrue);
        final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
        await mouse.addPointer(location: const Offset(1, 1));
        final pauses = scene.pauseCount, drags = scene.drags;
        final initialSeek = scene.lastSeek;
        await mouse.moveTo(
          tester.getCenter(find.byKey(const Key('media-scrubber'))),
        );
        await tester.pump(const Duration(milliseconds: 80));
        expect(scene.lastSeek, initialSeek);
        expect(scene.value.playing, isTrue);
        expect(scene.pauseCount, pauses);
        expect(scene.drags, drags);
        _expectSceneExposed(tester);
        await mouse.removePointer();
        await tester.pump();
        Focus.of(tester.element(find.byKey(const Key('scene-input'))))
            .requestFocus();
        await tester.pump();
        await tester.sendKeyEvent(LogicalKeyboardKey.space);
        await tester.pumpAndSettle();
        expect(scene.value.playing, isFalse);
        await tester.sendKeyEvent(LogicalKeyboardKey.space);
        await tester.pumpAndSettle();
        final scrubber = tester.getRect(
          find.byKey(const Key('media-scrubber')),
        );
        await tester.dragFrom(
          Offset(scrubber.left + 22, scrubber.center.dy),
          const Offset(80, 0),
        );
        await tester.pumpAndSettle();
        expect(scene.lastSeek, isNotNull);

        await tester.tap(find.byTooltip('共有'));
        await tester.pumpAndSettle();
        _expectModalProtected(tester);
        await tester.tap(find.text('リンクをコピー'));
        await tester.pumpAndSettle();
        expect(Uri.parse(copied!).queryParameters['clip'], 'first');
        await tester.tap(find.text('端末の共有を開く'));
        await tester.pumpAndSettle();
        expect(shared, 1);
        await tester.tap(find.byTooltip('閉じる'));
        await tester.pumpAndSettle();
        _expectSceneExposed(tester);
        expect(scene.value.playing, isTrue);

        await tester.tap(find.byTooltip('概要'));
        await tester.pumpAndSettle();
        _expectModalProtected(tester);
        expect(find.text('出典 CC BY 4.0'), findsOneWidget);
        await tester.tapAt(const Offset(20, 20));
        await tester.pumpAndSettle();
        expect(find.byType(BottomSheet), findsNothing);
        _expectSceneExposed(tester);

        await tester.tap(mode);
        await tester.pumpAndSettle();
        expect(scene.paused, isTrue);
        expect(scene.disposed, isFalse);
        expect(
          find.byKey(const Key('scene-input')),
          findsOneWidget,
        ); // Retained hidden scene.
        expect(find.byType(BottomSheet), findsNothing);
        expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
      },
    );
  }

  testWidgets('GS comments and account/search dialogs protect modal input', (
    tester,
  ) async {
    final scene = _Scene();
    await _pumpFreeView(tester, scene, const Size(390, 844));
    for (final tooltip in ['コメント', 'ログイン', '検索']) {
      if (tooltip == 'コメント') {
        await tester.tap(find.byTooltip('概要'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('コメントを見る'));
      } else {
        await tester.tap(find.byTooltip(tooltip));
      }
      await tester.pumpAndSettle();
      _expectModalProtected(tester);
      if (tooltip == '検索') {
        await tester.enterText(find.byType(TextFormField), '#自由視点');
        await tester.tap(find.text('キャンセル'));
      } else {
        await tester.tap(find.byTooltip('閉じる'));
      }
      await tester.pumpAndSettle();
      _expectSceneExposed(tester);
    }
    final before = scene.drags;
    await _dragScene(tester, _expectSceneExposed(tester));
    expect(scene.drags, greaterThan(before));
    expect(
      tester
          .widget<IconButton>(
            find.byWidgetPredicate(
              (w) => w is IconButton && w.tooltip == '次のハイライト',
            ),
          )
          .onPressed,
      isNull,
    );
    await tester.tap(find.text('動画に戻る'));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('次のハイライト'));
    await tester.pumpAndSettle();
    expect(scene.disposed, isTrue);
    expect(find.text('second'), findsOneWidget);
  });
}
