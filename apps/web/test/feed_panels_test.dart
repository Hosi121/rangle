import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/sharing/share_clip.dart';

import 'support/fake_viewer.dart';

class _PanelRepository implements ClipRepository {
  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async => [
    Clip(
      id: 'basketball',
      title: 'スポーツの決定的瞬間を、自由な視点で追体験するハイライト',
      sport: 'バスケットボール',
      tags: ['とても長いハッシュタグでも画面からはみ出さずに表示します'],
      description: '${'出典と再構成に関する詳細な説明。\n' * 40}CC BY 4.0',
      previewUrl: Uri.parse('https://example.invalid/video.webm'),
    ),
  ];
}

Future<void> _pumpFeed(
  WidgetTester tester, {
  Size size = const Size(390, 844),
  double textScale = 1,
  ClipShareAction? share,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  tester.platformDispatcher.textScaleFactorTestValue = textScale;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
  await tester.pumpWidget(
    RangleApp(
      repository: _PanelRepository(),
      viewerFactory: (_) => FakeViewer(),
      sceneFactory: (_) => FakeViewer(),
      shareAction: share ?? (_) async => ShareResult.cancelled,
    ),
  );
  await tester.pumpAndSettle();
}

void _expectPlayerWidth(WidgetTester tester) {
  final sheet = tester.getRect(
    find
        .descendant(
          of: find.byType(BottomSheet),
          matching: find.byType(Material),
        )
        .first,
  );
  final player = tester.getRect(find.byKey(const Key('feed-player')));
  expect(sheet.width, closeTo(player.width, .01));
  expect(sheet.center.dx, closeTo(player.center.dx, .01));
}

void main() {
  for (final size in [
    const Size(320, 640),
    const Size(390, 844),
    const Size(1440, 900),
  ]) {
    testWidgets('sharing at $size opens an app panel before native sharing', (
      tester,
    ) async {
      var nativeCalls = 0;
      String? copied;
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      messenger.setMockMethodCallHandler(SystemChannels.platform, (call) async {
        if (call.method == 'Clipboard.setData') {
          copied = (call.arguments as Map)['text'] as String;
        }
        return null;
      });
      addTearDown(
        () => messenger.setMockMethodCallHandler(SystemChannels.platform, null),
      );
      await _pumpFeed(
        tester,
        size: size,
        share: (clip) async {
          nativeCalls++;
          expect(clip.id, 'basketball');
          return ShareResult.shared;
        },
      );
      await tester.tap(find.byTooltip('共有'));
      await tester.pumpAndSettle();
      _expectPlayerWidth(tester);
      expect(find.text('ハイライトを共有'), findsOneWidget);
      expect(nativeCalls, 0);
      await tester.tap(find.widgetWithText(FilledButton, 'リンクをコピー'));
      await tester.pumpAndSettle();
      expect(Uri.parse(copied!).path, '/demo/');
      expect(Uri.parse(copied!).queryParameters, {'clip': 'basketball'});
      expect(find.text('リンクをコピーしました'), findsOneWidget);
      expect(nativeCalls, 0);
      await tester.tap(find.widgetWithText(OutlinedButton, '端末の共有を開く'));
      await tester.pumpAndSettle();
      expect(nativeCalls, 1);
      expect(find.text('共有しました'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets(
    'cancelled or failed sharing keeps the selectable link available',
    (tester) async {
      var attempts = 0;
      await _pumpFeed(
        tester,
        share: (_) async {
          if (++attempts == 1) return ShareResult.cancelled;
          throw PlatformException(code: 'unavailable');
        },
      );
      await tester.tap(find.byTooltip('共有'));
      await tester.pumpAndSettle();
      final native = find.widgetWithText(OutlinedButton, '端末の共有を開く');
      await tester.tap(native);
      await tester.pumpAndSettle();
      expect(find.text('共有しました'), findsNothing);
      expect(find.text('共有できませんでした。リンクを選択してコピーできます。'), findsNothing);
      await tester.tap(native);
      await tester.pumpAndSettle();
      expect(find.text('共有できませんでした。リンクを選択してコピーできます。'), findsOneWidget);
      expect(find.byKey(const Key('share-link')), findsOneWidget);
      await tester.tap(find.byTooltip('閉じる'));
      await tester.pumpAndSettle();
      expect(find.byType(BottomSheet), findsNothing);
    },
  );

  for (final size in [
    const Size(320, 640),
    const Size(390, 844),
    const Size(844, 390),
    const Size(1440, 900),
  ]) {
    testWidgets(
      'details at $size fit the player and keep close visible at 2x',
      (tester) async {
        await _pumpFeed(tester, size: size, textScale: 2);
        await tester.tap(find.byTooltip('概要'));
        await tester.pumpAndSettle();
        _expectPlayerWidth(tester);
        final close = tester.getRect(find.byTooltip('閉じる'));
        final viewport = Offset.zero & size;
        expect(viewport.contains(close.topLeft), isTrue);
        expect(viewport.contains(close.bottomRight), isTrue);
        await tester.drag(
          find.byType(SingleChildScrollView),
          const Offset(0, -300),
        );
        await tester.pumpAndSettle();
        expect(tester.getRect(find.byTooltip('閉じる')), close);
        final scrollable = tester.state<ScrollableState>(
          find
              .descendant(
                of: find.byType(SingleChildScrollView),
                matching: find.byType(Scrollable),
              )
              .first,
        );
        expect(scrollable.position.pixels, greaterThan(0));
        expect(find.textContaining('CC BY 4.0'), findsOneWidget);
        await tester.tap(find.byTooltip('閉じる'));
        await tester.pumpAndSettle();
        expect(find.byType(BottomSheet), findsNothing);
        expect(tester.takeException(), isNull);
      },
    );
  }
}
