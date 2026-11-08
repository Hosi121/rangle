import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:pointer_interceptor/pointer_interceptor.dart';
import 'package:rangle_web/community/account_controller.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/generation/generation_screen.dart';
import 'package:rangle_web/main.dart';

import 'support/fake_viewer.dart';
import 'support/generation_fixture.dart';

Future<AccountController> _show(
  WidgetTester tester,
  GenerationFixture fixture, {
  Size size = const Size(390, 844),
  double scale = 1,
  ValueChanged<Uri>? onOpen,
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  final account = AccountController(fixture.community);
  await account.initialize();
  addTearDown(account.dispose);
  await tester.pumpWidget(
    MaterialApp(
      home: MediaQuery(
        data: MediaQueryData(size: size, textScaler: TextScaler.linear(scale)),
        child: GenerationScreen(
          account: account,
          api: fixture.api,
          onOpenResult: onOpen ?? (_) {},
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return account;
}

Future<void> _tap(WidgetTester tester, String text) async {
  final target = find.text(text);
  if (target.evaluate().isEmpty) {
    await tester.scrollUntilVisible(
      target,
      200,
      scrollable: find.byType(Scrollable).first,
    );
  }
  await tester.ensureVisible(target);
  await tester.tap(target);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'two activations before rebuilding still create one generation request',
    (tester) async {
      final fixture = GenerationFixture()
        ..pendingCreate = Completer<http.Response>();
      await _show(tester, fixture);
      final activate = tester
          .widget<FilledButton>(find.widgetWithText(FilledButton, '生成を開始'))
          .onPressed!;
      activate();
      activate();
      await tester.pumpAndSettle();
      expect(fixture.creates, hasLength(1));
      fixture.pendingCreate!.complete(
        fixture.reply(generationResult('queued'), 202),
      );
      await tester.pumpAndSettle();
    },
  );

  testWidgets(
    'reviewed preset generates once, polls while visible and opens the completed viewer',
    (tester) async {
      final fixture = GenerationFixture();
      Uri? opened;
      await _show(tester, fixture, onOpen: (uri) => opened = uri);
      expect(find.textContaining('静的な3DGS'), findsOneWidget);
      expect(find.textContaining('CC BY 4.0'), findsOneWidget);
      fixture.pendingCreate = Completer<http.Response>();
      await _tap(tester, '生成を開始');
      expect(fixture.creates, hasLength(1));
      final button = find.widgetWithText(FilledButton, '依頼を送信しています');
      expect(tester.widget<FilledButton>(button).onPressed, isNull);
      expect(
        fixture.creates.single['request_id'],
        matches(RegExp(r'^[a-f0-9]{32}$')),
      );
      fixture.state = 'queued';
      fixture.pendingCreate!.complete(
        fixture.reply(generationResult('queued'), 202),
      );
      await tester.pumpAndSettle();
      expect(find.text('順番を待っています'), findsOneWidget);
      fixture.state = 'running';
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(fixture.polls, 1);
      expect(find.text('3DGSを生成しています'), findsOneWidget);
      fixture.state = 'succeeded';
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      await _tap(tester, '結果を見る');
      expect(opened.toString(), '/demo/?clip=generated-$generationJobId');
      await tester.pump(const Duration(seconds: 20));
      expect(fixture.polls, 2);
    },
  );

  testWidgets('ambiguous submission retries with the same request id', (
    tester,
  ) async {
    final fixture = GenerationFixture()..dropFirstCreate = true;
    await _show(tester, fixture);
    await _tap(tester, '生成を開始');
    expect(find.textContaining('接続を確認'), findsOneWidget);
    await _tap(tester, '同じ依頼で再試行');
    expect(fixture.creates, hasLength(2));
    expect(fixture.creates[0], fixture.creates[1]);
    expect(find.text('順番を待っています'), findsOneWidget);
  });

  testWidgets(
    'hidden screens stop polling, resume refreshes, disposal stops all work',
    (tester) async {
      final fixture = GenerationFixture()..state = 'running';
      await _show(tester, fixture);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      await tester.pump(const Duration(seconds: 20));
      expect(fixture.polls, 0);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(fixture.polls, 1);
      await tester.pumpWidget(const SizedBox());
      await tester.pump(const Duration(seconds: 20));
      expect(fixture.polls, 1);
    },
  );

  testWidgets(
    'cancel confirms the operation and keeps polling until cancellation completes',
    (tester) async {
      final fixture = GenerationFixture()..state = 'running';
      await _show(tester, fixture);
      await _tap(tester, '生成を中止');
      expect(find.byType(AlertDialog), findsOneWidget);
      await tester.pump(const Duration(seconds: 8));
      expect(fixture.polls, 0);
      await _tap(tester, '中止する');
      expect(fixture.deletes, 1);
      expect(find.text('中止しています'), findsOneWidget);
      fixture.state = 'cancelled';
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(find.text('生成を中止しました'), findsOneWidget);
    },
  );

  testWidgets(
    'anonymous users reuse the account dialog and logout clears owned jobs',
    (tester) async {
      final fixture = GenerationFixture()..user = null;
      final account = await _show(tester, fixture);
      await _tap(tester, 'ログインして生成する');
      await tester.enterText(
        find.byKey(const Key('account-username')),
        'viewer',
      );
      await tester.enterText(
        find.byKey(const Key('account-password')),
        'long test password 2026',
      );
      await _tap(tester, 'ログインする');
      expect(find.text('生成を開始'), findsOneWidget);
      await _tap(tester, '生成を開始');
      await tester.scrollUntilVisible(
        find.text('順番を待っています'),
        200,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('順番を待っています'), findsOneWidget);
      account.expired();
      await tester.pumpAndSettle();
      expect(find.text('順番を待っています'), findsNothing);
      expect(find.text('ログインして生成する'), findsOneWidget);
    },
  );

  for (final error in ['generation_busy', 'rate_limited']) {
    testWidgets('$error is shown next to the generation action', (
      tester,
    ) async {
      final fixture = GenerationFixture()..createError = error;
      await _show(tester, fixture);
      await _tap(tester, '生成を開始');
      expect(
        find.text(
          error == 'generation_busy'
              ? '別の生成が進行中です。少し時間をおいてお試しください。'
              : '操作が続いています。少し時間をおいてお試しください。',
        ),
        findsOneWidget,
      );
      expect(find.text('同じ依頼で再試行'), findsOneWidget);
    });
  }

  testWidgets('disabled generation is a recoverable empty state', (
    tester,
  ) async {
    final fixture = GenerationFixture()..presetsStatus = 404;
    await _show(tester, fixture);
    expect(find.textContaining('生成機能は準備中'), findsOneWidget);
    expect(find.text('生成を開始'), findsNothing);
    fixture.presetsStatus = null;
    await _tap(tester, '再読み込み');
    expect(find.text('生成を開始'), findsOneWidget);
  });

  testWidgets(
    'late submission responses cannot restore a signed-out owners jobs',
    (tester) async {
      final fixture = GenerationFixture()
        ..pendingCreate = Completer<http.Response>();
      final account = await _show(tester, fixture);
      await _tap(tester, '生成を開始');
      account.expired();
      await tester.pumpAndSettle();
      fixture.pendingCreate!.complete(
        fixture.reply(generationResult('queued'), 202),
      );
      await tester.pumpAndSettle();
      expect(find.text('ログインして生成する'), findsOneWidget);
      expect(find.text('順番を待っています'), findsNothing);
      expect(find.text('同じ依頼で再試行'), findsNothing);
    },
  );

  testWidgets(
    'polling backs off after rate limits and expires the existing account session on 401',
    (tester) async {
      final fixture = GenerationFixture()
        ..state = 'running'
        ..pollError = 'rate_limited';
      final account = await _show(tester, fixture);
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(fixture.polls, 1);
      expect(find.text('操作が続いています。少し時間をおいてお試しください。'), findsOneWidget);
      await tester.pump(const Duration(seconds: 4));
      expect(fixture.polls, 1);
      fixture.pollError = 'not_authenticated';
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(account.user, isNull);
      expect(find.text('3DGSを生成しています'), findsNothing);
      expect(find.text('ログインして生成する'), findsOneWidget);
    },
  );

  for (final size in [
    const Size(320, 640),
    const Size(844, 390),
    const Size(1440, 900),
  ]) {
    testWidgets('generation layout remains usable at $size and 2x text', (
      tester,
    ) async {
      await _show(tester, GenerationFixture(), size: size, scale: 2);
      await _tap(tester, '生成を開始');
      await tester.scrollUntilVisible(
        find.text('順番を待っています'),
        200,
        scrollable: find.byType(Scrollable).first,
      );
      expect(find.text('順番を待っています'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets(
    'feed entry releases media while generation is open and reuses the session',
    (tester) async {
      final fixture = GenerationFixture();
      final viewers = <FakeViewer>[];
      final scene = FakeViewer(surfaceKey: 'generation-entry-scene');
      await tester.pumpWidget(
        RangleApp(
          repository: const DemoClipRepository(
            sceneUrl: 'https://example.invalid/scene.json',
          ),
          community: fixture.community,
          generation: fixture.api,
          viewerFactory: (_) {
            final viewer = FakeViewer();
            viewers.add(viewer);
            return viewer;
          },
          sceneFactory: (_) => scene,
        ),
      );
      await tester.pumpAndSettle();
      final mode = find.text('自由視点で見る');
      await tester.tap(
        mode.evaluate().isEmpty ? find.byTooltip('自由視点で見る') : mode,
      );
      await tester.pumpAndSettle();
      expect(
        find.ancestor(
          of: find.byTooltip('3DGSを生成'),
          matching: find.byWidgetPredicate(
            (widget) => widget is PointerInterceptor && widget.intercepting,
          ),
        ),
        findsOneWidget,
      );
      await tester.tap(find.byTooltip('3DGSを生成'));
      await tester.pumpAndSettle();
      expect(viewers.first.disposed, isTrue);
      expect(scene.disposed, isTrue);
      expect(find.byType(GenerationScreen), findsOneWidget);
      expect(find.text('生成を開始'), findsOneWidget);
      expect(
        fixture.requests.where(
          (request) => request.url.path.endsWith('/auth/me'),
        ),
        hasLength(1),
      );
      await tester.tap(find.byType(BackButton));
      await tester.pumpAndSettle();
      expect(viewers, hasLength(2));
      expect(find.byType(GenerationScreen), findsNothing);
    },
  );

  testWidgets(
    'direct generation entry opens below demo without an extra authentication flow',
    (tester) async {
      final fixture = GenerationFixture();
      await tester.pumpWidget(
        RangleApp(
          repository: const DemoClipRepository(),
          community: fixture.community,
          generation: fixture.api,
          initialGeneration: true,
          viewerFactory: (_) => FakeViewer(),
          sceneFactory: (_) => FakeViewer(),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byType(GenerationScreen), findsOneWidget);
      expect(find.text('生成を開始'), findsOneWidget);
      expect(
        fixture.requests.where(
          (request) => request.url.path.endsWith('/auth/me'),
        ),
        hasLength(1),
      );
    },
  );
}
