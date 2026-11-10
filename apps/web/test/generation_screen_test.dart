import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';
import 'package:rangle_web/community/account_controller.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/generation/generation_screen.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/live/live_api.dart';
import 'package:rangle_web/live/live_screen.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';

import 'support/fake_viewer.dart';
import 'support/generation_fixture.dart';

class _GeneratedClip implements ClipRepository {
  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async => [
    Clip(
      id: 'generated-$generationJobId',
      title: '生成した3DGS',
      sport: 'バスケットボール',
      description: '静的3DGS',
      previewUrl: Uri.parse('https://example.invalid/source.webm'),
      sceneUrl: Uri.parse('https://example.invalid/scene.json'),
    ),
  ];
}

class _LiveFeed {
  bool ready = false;
  int polls = 0;
  late final api = LiveApi(
    endpoint: Uri.parse('https://app.example.invalid/api/v1/live'),
    client: MockClient((_) async {
      polls++;
      if (!ready) return http.Response('{}', 404);
      final digest = 'a' * 64;
      return http.Response.bytes(
        utf8.encode(
          jsonEncode({
            'id': generationJobId,
            'title': 'スポーツのLIVE',
            'state': 'live',
            'started_at': '2026-11-09T00:00:00Z',
            'updated_at': '2026-11-09T00:00:01Z',
            'latest_input_time_seconds': .76,
            'target_latency_seconds': 30,
            'dropped_windows': 0,
            'source': {
              'label': 'Test source',
              'url': 'https://source.example.invalid',
              'license': 'CC BY 4.0',
              'caveat': 'Synthetic test source.',
            },
            'segments': [
              {
                'sequence': 1,
                'start_seconds': 0,
                'end_seconds': .76,
                'scene_url':
                    '/live-assets/$generationJobId/000001-${digest.substring(0, 16)}/scene.json',
                'model_sha256': digest,
                'model_bytes': 1000,
                'captured_at': '2026-11-09T00:00:00Z',
                'available_at': '2026-11-09T00:00:01Z',
                'training_seconds': 0.1,
                'mean_packed_psnr': 20,
                'mean_packed_ssim': 0.8,
              },
            ],
          }),
        ),
        200,
        headers: {'content-type': 'application/json'},
      );
    }),
  );
}

Future<AccountController> _show(
  WidgetTester tester,
  GenerationFixture fixture, {
  Size size = const Size(390, 844),
  double scale = 1,
  ValueChanged<Uri>? onOpen,
  VoidCallback? onPublicationChanged,
  LiveApi? live,
  ViewerFactory? liveFactory,
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
        data: MediaQueryData(
          size: size,
          textScaler: TextScaler.linear(scale),
          disableAnimations: true,
        ),
        child: GenerationScreen(
          account: account,
          api: fixture.api,
          onOpenResult: onOpen ?? (_) {},
          onPublicationChanged: onPublicationChanged,
          live: live,
          liveFactory: liveFactory,
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
  await tester.pumpAndSettle();
  await tester.tap(target);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'LIVE starts through admission and retains job status and stop access before and after the first snapshot',
    (tester) async {
      final fixture = GenerationFixture()..mode = 'live';
      final feed = _LiveFeed();
      final viewers = <FakeViewer>[];
      await _show(
        tester,
        fixture,
        live: feed.api,
        liveFactory: (_) {
          final viewer = FakeViewer(surfaceKey: 'cloud-live-surface');
          viewers.add(viewer);
          return viewer;
        },
        onOpen: (_) => fail('LIVE must keep job controls in the app'),
      );
      expect(find.text('LIVEの自由視点（順次更新）'), findsOneWidget);
      await _tap(tester, 'LIVEを開始');
      expect(fixture.creates, hasLength(1));
      await _tap(tester, 'LIVEを見る');
      expect(find.byType(LiveScreen), findsOneWidget);
      expect(find.text('LIVE用GPUの準備を待っています'), findsOneWidget);
      expect(find.text('GPUと配信の準備をしています'), findsOneWidget);
      expect(find.text('LIVEを停止'), findsOneWidget);
      final firstPolls = feed.polls;
      await tester.pump(const Duration(seconds: 1));
      await tester.pumpAndSettle();
      expect(feed.polls, greaterThan(firstPolls));
      fixture.state = 'running';
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(find.text('LIVEの生成ジョブが稼働中です'), findsOneWidget);
      expect(find.text('GPUと配信の準備をしています'), findsOneWidget);
      feed.ready = true;
      await tester.pump(const Duration(seconds: 1));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('cloud-live-surface')), findsOneWidget);
      expect(viewers, hasLength(1));
      await _tap(tester, 'LIVEを停止');
      expect(find.text('LIVEの生成を停止しますか？'), findsOneWidget);
      await _tap(tester, '中止する');
      expect(fixture.deletes, 1);
      expect(find.text('中止しています'), findsOneWidget);
      expect(viewers.single.disposed, isFalse);
      fixture.state = 'cancelled';
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(find.text('生成を中止しました'), findsOneWidget);
      expect(find.text('LIVEを停止'), findsNothing);
      expect(find.text('フィードに掲載'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
      expect(viewers.single.disposed, isTrue);
      final polls = feed.polls, jobs = fixture.polls;
      await tester.pump(const Duration(seconds: 12));
      expect(feed.polls, polls);
      expect(fixture.polls, jobs);
    },
  );

  for (final state in ['queued', 'succeeded', 'failed', 'cancelled']) {
    testWidgets(
      'LIVE history in $state can reopen its viewer without VOD publication',
      (tester) async {
        final fixture = GenerationFixture()
          ..mode = 'live'
          ..state = state;
        final feed = _LiveFeed();
        await _show(tester, fixture, live: feed.api);
        expect(find.text('結果を見る'), findsNothing);
        expect(find.text('フィードに掲載'), findsNothing);
        await _tap(tester, 'LIVEを見る');
        expect(
          fixture.polls,
          1,
          reason: 'History reconnect retrieves the trusted wrapper viewer URL',
        );
        expect(find.byType(LiveScreen), findsOneWidget);
        final polls = feed.polls;
        await tester.pump(const Duration(seconds: 1));
        await tester.pumpAndSettle();
        expect(feed.polls, state == 'queued' ? greaterThan(polls) : polls);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }

  testWidgets(
    'LIVE deep link reload restores owned job controls, pauses hidden work and clears ownership on logout',
    (tester) async {
      final fixture = GenerationFixture()
        ..mode = 'live'
        ..state = 'queued';
      final feed = _LiveFeed();
      await tester.pumpWidget(
        RangleApp(
          repository: const DemoClipRepository(),
          community: fixture.community,
          generation: fixture.api,
          live: feed.api,
          initialLiveId: generationJobId,
          viewerFactory: (_) => FakeViewer(),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('管理'), findsOneWidget);
      await _tap(tester, 'LIVEを見る');
      expect(find.text('LIVE用GPUの準備を待っています'), findsOneWidget);
      expect(find.text('LIVEを停止'), findsOneWidget);
      expect(
        fixture.requests.any((r) => r.url.path.endsWith('/generation/presets')),
        isTrue,
      );
      expect(fixture.creates, isEmpty);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      await tester.pumpAndSettle();
      final polls = feed.polls, jobs = fixture.polls;
      await tester.pump(const Duration(seconds: 8));
      expect(feed.polls, polls);
      expect(fixture.polls, jobs);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      await tester.pumpAndSettle();
      expect(feed.polls, greaterThan(polls));
      tester
          .widget<GenerationScreen>(find.byType(GenerationScreen))
          .account!
          .expired();
      await tester.pumpAndSettle();
      expect(find.text('LIVEを停止'), findsNothing);
      expect(find.text('GPUと配信の準備をしています'), findsNothing);
      final afterLogout = feed.polls;
      await tester.pump(const Duration(seconds: 5));
      expect(feed.polls, afterLogout);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  for (final size in [const Size(320, 640), const Size(844, 390)]) {
    testWidgets(
      'pending LIVE retains usable job controls at $size and 2x text',
      (tester) async {
        final fixture = GenerationFixture()
          ..mode = 'live'
          ..state = 'queued';
        await _show(
          tester,
          fixture,
          live: _LiveFeed().api,
          size: size,
          scale: 2,
        );
        await _tap(tester, 'LIVEを見る');
        expect(find.text('LIVEを停止'), findsOneWidget);
        expect(tester.takeException(), isNull);
        await _tap(tester, 'LIVEを停止');
        expect(find.byType(AlertDialog), findsOneWidget);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }

  testWidgets(
    'successful results can be listed and withdrawn without a GPU job',
    (tester) async {
      final fixture = GenerationFixture()..state = 'succeeded';
      var changes = 0;
      await _show(tester, fixture, onPublicationChanged: () => changes++);
      await _tap(tester, 'フィードに掲載');
      expect(fixture.publications, [true]);
      expect(changes, 1);
      expect(find.text('フィードに掲載中'), findsOneWidget);
      expect(find.textContaining('取り下げ後も結果を開けます'), findsOneWidget);
      await _tap(tester, '掲載を取り下げる');
      expect(fixture.publications, [true, false]);
      expect(changes, 2);
      expect(find.text('フィードに掲載中'), findsNothing);
      expect(fixture.creates, isEmpty);
    },
  );

  testWidgets(
    'ambiguous listing retries the same intent and reports failures',
    (tester) async {
      final fixture = GenerationFixture()
        ..state = 'succeeded'
        ..dropFirstPublication = true;
      var changes = 0;
      await _show(tester, fixture, onPublicationChanged: () => changes++);
      await _tap(tester, 'フィードに掲載');
      expect(fixture.published, isTrue);
      expect(changes, 0);
      expect(find.textContaining('接続を確認'), findsOneWidget);
      await _tap(tester, 'フィードに掲載');
      expect(fixture.publications, [true, true]);
      expect(changes, 1);
      fixture.publicationError = 'publication_unavailable';
      await _tap(tester, '掲載を取り下げる');
      expect(find.text('フィードに掲載中'), findsOneWidget);
      expect(find.textContaining('掲載するデータを確認できませんでした'), findsOneWidget);
      expect(changes, 1);
    },
  );

  testWidgets('late publication responses cannot restore a signed-out owner', (
    tester,
  ) async {
    final fixture = GenerationFixture()
      ..state = 'succeeded'
      ..pendingPublication = Completer<http.Response>();
    var changes = 0;
    final account = await _show(
      tester,
      fixture,
      onPublicationChanged: () => changes++,
    );
    await _tap(tester, 'フィードに掲載');
    final button = find.widgetWithText(OutlinedButton, '状態を確認しています');
    expect(tester.widget<OutlinedButton>(button).onPressed, isNull);
    await account.logout();
    await tester.pumpAndSettle();
    fixture.pendingPublication!.complete(
      fixture.reply(generationResult('succeeded', published: true)),
    );
    await tester.pumpAndSettle();
    expect(changes, 0);
    expect(find.text('フィードに掲載中'), findsNothing);
    expect(find.text('ログイン'), findsOneWidget);
  });

  testWidgets(
    'an unconfirmed publication response retries the same boolean intent',
    (tester) async {
      final fixture = GenerationFixture()
        ..state = 'succeeded'
        ..pendingPublication = Completer<http.Response>();
      var changes = 0;
      await _show(tester, fixture, onPublicationChanged: () => changes++);
      await _tap(tester, 'フィードに掲載');
      fixture.published = true;
      final result = generationResult('succeeded', published: true);
      (result['job'] as Map<String, Object?>).remove('published');
      fixture.pendingPublication!.complete(fixture.reply(result));
      await tester.pumpAndSettle();
      expect(changes, 0);
      expect(find.textContaining('掲載するデータを確認できませんでした'), findsOneWidget);
      fixture.pendingPublication = null;
      await _tap(tester, 'フィードに掲載');
      expect(fixture.publications, [true, true]);
      expect(changes, 1);
      expect(find.text('フィードに掲載中'), findsOneWidget);
    },
  );

  testWidgets('running jobs do not offer listing controls', (tester) async {
    final fixture = GenerationFixture()..state = 'running';
    await _show(tester, fixture);
    expect(find.text('フィードに掲載'), findsNothing);
    expect(find.text('掲載を取り下げる'), findsNothing);
  });

  testWidgets('dynamic preset and active job are labeled as moving 4DGS', (
    tester,
  ) async {
    final fixture = GenerationFixture()
      ..mode = 'dynamic'
      ..state = 'running';
    await _show(tester, fixture);
    expect(find.text('動く4DGS（複数時刻）'), findsOneWidget);
    expect(find.textContaining('対応していません'), findsNothing);
    final state = find.text('4DGSを生成しています');
    await tester.scrollUntilVisible(
      state,
      200,
      scrollable: find.byType(Scrollable).first,
    );
    expect(state, findsOneWidget);
    expect(find.text('3DGSを生成しています'), findsNothing);
  });

  testWidgets(
    'generated result deep links create the scene before any input video',
    (tester) async {
      final factories = <String>[];
      final scene = FakeViewer(
        initialStatus: ViewerStatus.loading,
        surfaceKey: 'generated-scene',
        canPlay: false,
      );
      await tester.pumpWidget(
        RangleApp(
          repository: _GeneratedClip(),
          initialClipId: 'generated-$generationJobId',
          viewerFactory: (_) {
            factories.add('video');
            return FakeViewer();
          },
          sceneFactory: (_) {
            factories.add('scene');
            return scene;
          },
        ),
      );
      await tester.pumpAndSettle();
      expect(factories, ['scene']);
      expect(find.text('自由視点を読み込んでいます'), findsOneWidget);
      expect(find.byKey(const Key('real-video-surface')), findsNothing);
      scene.becomeReady();
      await tester.pumpAndSettle();
      expect(factories, ['scene']);
      expect(find.byKey(const Key('generated-scene')), findsOneWidget);
      expect(
        tester
            .widget<PageView>(find.byKey(const Key('highlight-feed')))
            .physics,
        isA<NeverScrollableScrollPhysics>(),
      );
      final back = find.text('動画に戻る');
      await tester.tap(
        back.evaluate().isEmpty ? find.byTooltip('動画に戻る') : back,
      );
      await tester.pumpAndSettle();
      expect(factories, ['scene', 'video']);
    },
  );

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
      await _tap(tester, 'ログイン');
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
      expect(find.text('ログイン'), findsOneWidget);
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
      expect(find.text('ログイン'), findsOneWidget);
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
      expect(find.text('ログイン'), findsOneWidget);
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
    'feed LIVE entry releases media while open and reuses the existing account session',
    (tester) async {
      final fixture = GenerationFixture(admin: false);
      final viewers = <FakeViewer>[];
      final scene = FakeViewer(surfaceKey: 'live-entry-scene');
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
          of: find.byTooltip('LIVEを開く'),
          matching: find.byWidgetPredicate(
            (widget) => widget is PointerInterceptor && widget.intercepting,
          ),
        ),
        findsOneWidget,
      );
      await tester.tap(find.byTooltip('LIVEを開く'));
      await tester.pumpAndSettle();
      expect(viewers.first.disposed, isTrue);
      expect(scene.disposed, isTrue);
      expect(find.byType(GenerationScreen), findsOneWidget);
      expect(find.byType(LiveScreen), findsOneWidget);
      expect(
        fixture.requests.where(
          (request) => request.url.path.endsWith('/auth/me'),
        ),
        hasLength(1),
      );
      await tester.tap(find.text('フィードに戻る'));
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
