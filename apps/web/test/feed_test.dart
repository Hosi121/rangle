import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/preview_viewer.dart';
import 'package:rangle_web/widgets/edge_seek_bar.dart';

import 'support/fake_viewer.dart';

class BufferedScene extends FakeViewer {
  BufferedScene()
    : super(
        initialStatus: ViewerStatus.loading,
        surfaceKey: 'real-scene-surface',
      );
  bool pendingFrame = false;
  @override
  ViewerValue get value {
    final v = super.value;
    return ViewerValue(
      status: v.status,
      position: v.position,
      duration: v.duration,
      playing: v.playing,
      canPlay: v.canPlay,
      muted: v.muted,
      buffering: pendingFrame,
    );
  }

  @override
  Future<void> seek(Duration position) async {
    pendingFrame = true;
    await super.seek(position);
  }

  void acknowledgeFrame() {
    pendingFrame = false;
    notifyListeners();
  }
}

class TestRepository implements ClipRepository {
  TestRepository(this.loadClips);
  final Future<List<Clip>> Function() loadClips;

  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) => loadClips();
}

Finder iconButton(String tooltip) => find.byWidgetPredicate(
  (widget) => widget is IconButton && widget.tooltip == tooltip,
);

Future<void> pumpApp(
  WidgetTester tester, {
  ClipRepository? repository,
  ViewerFactory? viewerFactory,
  ViewerFactory? sceneFactory,
  Size size = const Size(390, 844),
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    RangleApp(
      repository:
          repository ??
          TestRepository(
            () async => const [
              Clip(
                id: 'pending1',
                title: 'バスケットボール 01',
                sport: 'バスケットボール',
                description: '準備中',
              ),
              Clip(
                id: 'pending2',
                title: 'バスケットボール 02',
                sport: 'バスケットボール',
                description: '準備中',
              ),
              Clip(
                id: 'pending3',
                title: 'バスケットボール 03',
                sport: 'バスケットボール',
                description: '準備中',
              ),
            ],
          ),
      viewerFactory: viewerFactory,
      sceneFactory: sceneFactory,
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
    'explicit entry button also enters while the pointer stays down',
    (tester) async {
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) => FakeViewer(),
        sceneFactory: (_) => FakeViewer(),
      );
      final gesture = await tester.startGesture(
        tester.getCenter(find.text('自由視点で見る')),
      );
      await tester.pump(const Duration(milliseconds: 600));
      await tester.pumpAndSettle();
      expect(find.text('動画に戻る'), findsOneWidget);
      expect(find.text('自由視点'), findsNothing);
      expect(find.byIcon(Icons.info_outline), findsOneWidget);
      expect(find.byTooltip('概要'), findsOneWidget);
      await gesture.up();
      await tester.pumpAndSettle();
      expect(find.text('動画に戻る'), findsOneWidget);
    },
  );
  testWidgets('wheel momentum advances one clip per scroll gesture', (
    tester,
  ) async {
    await pumpApp(tester);
    final point = tester.getCenter(find.byKey(const Key('feed-player')));
    Future<void> wheel(double delta) async {
      await tester.sendEventToBinding(
        PointerScrollEvent(
          position: point,
          scrollDelta: Offset(0, delta),
          kind: PointerDeviceKind.mouse,
        ),
      );
      await tester.pump();
    }

    await wheel(100);
    await tester.pump(const Duration(milliseconds: 80));
    final page = tester
        .widget<PageView>(find.byKey(const Key('highlight-feed')))
        .controller!
        .page!;
    expect(page, greaterThan(0));
    expect(page, lessThan(1));
    for (var i = 0; i < 12; i++) {
      await wheel(100);
      await tester.pump(const Duration(milliseconds: 20));
    }
    await tester.pumpAndSettle();
    expect(
      feedPage(tester),
      1,
      reason:
          'Wheel gesture settled at page ${tester.widget<PageView>(find.byKey(const Key('highlight-feed'))).controller!.page}',
    );
    await tester.pump(const Duration(milliseconds: 260));
    await wheel(9000);
    await tester.pumpAndSettle();
    expect(feedPage(tester), 2);
    await tester.pump(const Duration(milliseconds: 260));
    await wheel(-120);
    await tester.pumpAndSettle();
    expect(feedPage(tester), 1);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets('pending media has disabled playback and free-view controls', (
    tester,
  ) async {
    var created = 0;
    await pumpApp(
      tester,
      viewerFactory: (_) {
        created++;
        return FakeViewer();
      },
    );
    expect(find.text('映像を準備しています'), findsOneWidget);
    expect(find.text('バスケットボール 01'), findsOneWidget);
    expect(created, 0);
    expect(tester.widget<IconButton>(iconButton('再生')).onPressed, isNull);
    expect(find.byType(EdgeSeekBar), findsNothing);
    expect(find.text('自由視点は準備中'), findsNothing);
  });

  testWidgets('buttons, swipe and keyboard navigate the finite feed', (
    tester,
  ) async {
    await pumpApp(tester);
    expect(tester.widget<IconButton>(iconButton('前のハイライト')).onPressed, isNull);
    await tester.tap(iconButton('次のハイライト'));
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 02'), findsOneWidget);
    await tester.drag(
      find.byKey(const Key('highlight-feed')),
      const Offset(0, -500),
    );
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 03'), findsOneWidget);
    expect(tester.widget<IconButton>(iconButton('次のハイライト')).onPressed, isNull);
    await tester.sendKeyEvent(LogicalKeyboardKey.arrowUp);
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 02'), findsOneWidget);
    await tester.tap(iconButton('前のハイライト'));
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 01'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'mobile media fills the viewport with controls over the surface',
    (tester) async {
      await pumpApp(
        tester,
        repository: const DemoClipRepository(),
        viewerFactory: (_) => FakeViewer(),
      );
      final player = tester.getRect(find.byKey(const Key('feed-player')));
      final media = tester.getRect(find.byKey(const Key('real-video-surface')));
      expect(player.size, const Size(390, 844));
      expect(media, player);
      expect(
        player.contains(
          tester.getCenter(find.byKey(const Key('video-playback-surface'))),
        ),
        isTrue,
      );
      expect(
        player.contains(tester.getCenter(find.text('VRU Basketball'))),
        isTrue,
      );
      expect(find.textContaining('CC BY 4.0'), findsNothing);
      expect(find.byIcon(Icons.info_outline), findsOneWidget);
      expect(find.textContaining(RegExp(r'^\d+ / \d+\+?$')), findsNothing);
      await tester.tap(iconButton('概要'));
      await tester.pumpAndSettle();
      expect(find.textContaining('CC BY 4.0'), findsOneWidget);
      final semantics = tester.ensureSemantics();
      try {
        await tester.pump();
        expect(
          tester
              .getSemantics(find.byType(SelectableText))
              .getSemanticsData()
              .label,
          contains('CC BY 4.0'),
        );
      } finally {
        semantics.dispose();
      }
      await tester.tap(iconButton('閉じる'));
      await tester.pumpAndSettle();
      expect(find.textContaining('CC BY 4.0'), findsNothing);
    },
  );

  testWidgets(
    'GS overlay and full source details remain usable at text scale 2',
    (tester) async {
      tester.platformDispatcher.textScaleFactorTestValue = 2;
      addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
      final scene = FakeViewer(surfaceKey: 'real-scene-surface');
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) => FakeViewer(),
        sceneFactory: (_) => scene,
      );
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('real-scene-surface')), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.tap(iconButton('概要'));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.textContaining('CC BY 4.0'));
      expect(find.textContaining('CC BY 4.0'), findsOneWidget);
      await tester.tap(iconButton('閉じる'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('動画に戻る'));
      await tester.pumpAndSettle();
      expect(scene.paused, isTrue);
      expect(scene.disposed, isFalse);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'desktop keeps one centered portrait player with overlay navigation',
    (tester) async {
      await pumpApp(tester, size: const Size(1440, 900));
      final player = tester.getRect(find.byKey(const Key('feed-player')));
      expect(player.height, 900);
      expect(player.width, closeTo(900 * 9 / 16, .01));
      expect(player.center.dx, 720);
      expect(find.byType(ListTile), findsNothing);
      expect(find.byIcon(Icons.info_outline), findsOneWidget);
      expect(find.textContaining(RegExp(r'^\d+ / \d+\+?$')), findsNothing);
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      expect(feedPage(tester), 2);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets('catalog load failures recover through reload', (tester) async {
    var calls = 0;
    final repository = TestRepository(() async {
      calls++;
      if (calls == 1) throw StateError('private diagnostic should stay hidden');
      return const DemoClipRepository().load();
    });
    await pumpApp(tester, repository: repository);
    expect(find.text('ハイライトを読み込めませんでした'), findsOneWidget);
    expect(find.textContaining('private diagnostic'), findsNothing);
    await tester.tap(find.text('再読み込み'));
    await tester.pumpAndSettle();
    expect(find.text('VRU Basketball'), findsOneWidget);
    expect(calls, 2);
  });

  testWidgets('empty feed offers refresh', (tester) async {
    var calls = 0;
    await pumpApp(
      tester,
      repository: TestRepository(() async {
        calls++;
        return [];
      }),
    );
    expect(find.text('公開されたハイライトはまだありません'), findsOneWidget);
    await tester.tap(find.text('更新する'));
    await tester.pumpAndSettle();
    expect(calls, 2);
  });

  testWidgets('catalog loading is explicit', (tester) async {
    final completer = Completer<List<Clip>>();
    await pumpApp(tester, repository: TestRepository(() => completer.future));
    expect(find.text('ハイライトを読み込んでいます'), findsOneWidget);
    completer.complete([]);
    await tester.pumpAndSettle();
    expect(find.text('公開されたハイライトはまだありません'), findsOneWidget);
  });

  testWidgets('play, scrub, mute and leaving a clip release its viewer', (
    tester,
  ) async {
    final viewers = <FakeViewer>[];
    await pumpApp(
      tester,
      repository: const DemoClipRepository(
        previewUrl: 'https://example.invalid/real.mp4',
      ),
      viewerFactory: (_) {
        final viewer = FakeViewer();
        viewers.add(viewer);
        return viewer;
      },
    );
    expect(viewers, hasLength(1));
    expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
    await tester.tap(find.byKey(const Key('video-playback-surface')));
    await tester.pump();
    expect(viewers.single.value.playing, isTrue);
    expect(find.byIcon(Icons.pause), findsOneWidget);
    await tester.tap(iconButton('ミュート'));
    await tester.pump();
    expect(viewers.single.value.muted, isTrue);
    await tester.tap(find.byKey(const Key('media-scrubber')).hitTestable());
    await tester.pump();
    expect(viewers.single.lastSeek, isNotNull);
    expect(viewers.single.lastSeek!.inMilliseconds, inInclusiveRange(1, 20000));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    expect(viewers.single.pauseCount, 1);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.tap(iconButton('次のハイライト'));
    await tester.pumpAndSettle();
    expect(viewers.single.disposed, isTrue);
    expect(find.byKey(const Key('real-video-surface')), findsNothing);
    await tester.tap(iconButton('前のハイライト'));
    await tester.pumpAndSettle();
    expect(viewers, hasLength(2));
  });

  testWidgets('media loading is disabled and errors can retry', (tester) async {
    final viewers = <FakeViewer>[];
    await pumpApp(
      tester,
      repository: const DemoClipRepository(
        previewUrl: 'https://example.invalid/real.mp4',
      ),
      viewerFactory: (_) {
        final viewer = FakeViewer(
          initialStatus: viewers.isEmpty
              ? ViewerStatus.loading
              : ViewerStatus.ready,
        );
        viewers.add(viewer);
        return viewer;
      },
    );
    expect(find.text('映像を読み込んでいます'), findsOneWidget);
    expect(find.byKey(const Key('video-playback-surface')), findsNothing);
    viewers.single.fail();
    await tester.pump();
    expect(find.text('映像を再生できませんでした'), findsOneWidget);
    await tester.tap(find.text('再試行'));
    await tester.pumpAndSettle();
    expect(viewers.first.disposed, isTrue);
    expect(viewers, hasLength(2));
    expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
  });

  testWidgets('small phone and landscape layouts do not overflow', (
    tester,
  ) async {
    await pumpApp(tester, size: const Size(320, 568));
    expect(tester.takeException(), isNull);
    tester.view.physicalSize = const Size(844, 390);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('large text remains usable on a phone', (tester) async {
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    await pumpApp(tester);
    expect(tester.takeException(), isNull);
    expect(iconButton('次のハイライト'), findsOneWidget);
  });

  testWidgets('configured preview fits the landscape layout', (tester) async {
    await pumpApp(
      tester,
      size: const Size(844, 390),
      repository: const DemoClipRepository(
        previewUrl: 'https://example.invalid/real.mp4',
      ),
      viewerFactory: (_) => FakeViewer(),
    );
    expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('pending screen meets tap target and label guidelines', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      await pumpApp(tester);
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    } finally {
      semantics.dispose();
    }
  });

  test(
    'invalid preview configuration is rejected rather than played',
    () async {
      await expectLater(
        const DemoClipRepository(previewUrl: 'file:///tmp/not-a-video.bin')
            .load(),
        throwsFormatException,
      );
    },
  );

  test(
    'bundled sample is an asset and external scene overrides require HTTP',
    () async {
      final clips = await const DemoClipRepository().load();
      expect(clips.first.title, 'VRU Basketball');
      expect(
        clips.first.previewUrl.toString(),
        'assets/samples/basketball.webm',
      );
      expect(clips.first.previewUrl!.hasScheme, isFalse);
      expect(clips.first.description, contains('CC BY 4.0'));
      expect(clips.first.sceneUrl, isNull);
      await expectLater(
        const DemoClipRepository(sceneUrl: 'file:///scene.json').load(),
        throwsFormatException,
      );
    },
  );

  testWidgets(
    'charged hold waits for the actual frame; return is immediate and navigation locks',
    (tester) async {
      final video = FakeViewer(), scene = BufferedScene();
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) => video,
        sceneFactory: (_) => scene,
      );
      await video.seek(const Duration(seconds: 7));
      await video.togglePlayback();
      await tester.pump();
      final gesture = await tester.startGesture(
        tester.getCenter(find.byKey(const Key('immersive-hold-area'))),
      );
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump(const Duration(milliseconds: 1300));
      await gesture.up();
      await tester.pump(const Duration(seconds: 4));
      expect(find.text('動画に戻る'), findsNothing);
      expect(video.value.playing, isFalse);
      expect(
        tester.widget<IconButton>(iconButton('次のハイライト')).onPressed,
        isNull,
      );
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowDown);
      expect(feedPage(tester), 0);
      scene.becomeReady();
      await tester.pump();
      await tester.pump();
      expect(scene.lastSeek, const Duration(seconds: 7));
      await tester.pump(const Duration(seconds: 2));
      expect(find.text('動画に戻る'), findsNothing);
      scene.acknowledgeFrame();
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 240));
      expect(find.text('動画に戻る'), findsNothing);
      await tester.pump(const Duration(milliseconds: 3000));
      await tester.pump();
      expect(find.text('動画に戻る'), findsOneWidget);
      expect(scene.value.playing, isTrue);
      expect(video.disposed, isFalse);
      await tester.drag(
        find.byKey(const Key('highlight-feed')),
        const Offset(0, -500),
      );
      await tester.pump();
      expect(feedPage(tester), 0);
      await scene.seek(const Duration(seconds: 9));
      await tester.tap(find.text('動画に戻る'));
      await tester.pumpAndSettle();
      expect(find.text('動画に戻る'), findsNothing);
      expect(video.lastSeek, const Duration(seconds: 9));
      expect(scene.paused, isTrue);
      expect(scene.disposed, isFalse);
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      expect(scene.disposed, isTrue);
    },
  );

  testWidgets(
    'short tap and early release do not enter; cancelled scene cannot complete later',
    (tester) async {
      final video = FakeViewer();
      final scenes = <FakeViewer>[];
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) => video,
        sceneFactory: (_) {
          final v = FakeViewer(initialStatus: ViewerStatus.loading);
          scenes.add(v);
          return v;
        },
      );
      final area = find.byKey(const Key('immersive-hold-area'));
      await tester.tap(area);
      await tester.pump();
      expect(scenes, isEmpty);
      final hold = await tester.startGesture(tester.getCenter(area));
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump(const Duration(milliseconds: 200));
      await hold.up();
      scenes.single.becomeReady();
      await tester.pumpAndSettle();
      expect(scenes.single.disposed, isTrue);
      expect(video.value.playing, isTrue);
      expect(find.text('動画に戻る'), findsNothing);
      expect(
        tester.widget<IconButton>(iconButton('次のハイライト')).onPressed,
        isNotNull,
      );
    },
  );

  testWidgets(
    'failed preparation restores video and retry replaces the scene',
    (tester) async {
      final video = FakeViewer();
      final scenes = <FakeViewer>[];
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) => video,
        sceneFactory: (_) {
          final v = FakeViewer(initialStatus: ViewerStatus.loading);
          scenes.add(v);
          return v;
        },
      );
      await tester.tap(find.text('自由視点で見る'));
      await tester.pump();
      scenes.single.fail();
      await tester.pumpAndSettle();
      expect(scenes.single.disposed, isTrue);
      expect(video.disposed, isFalse);
      expect(find.textContaining('自由視点を表示できませんでした'), findsOneWidget);
      await tester.tap(find.text('自由視点を再試行'));
      await tester.pump();
      expect(scenes, hasLength(2));
      scenes.last.becomeReady();
      await tester.pumpAndSettle();
      expect(find.text('動画に戻る'), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('explicit cancel releases pending resources and unlocks feed', (
    tester,
  ) async {
    final scene = FakeViewer(initialStatus: ViewerStatus.loading);
    await pumpApp(
      tester,
      repository: const DemoClipRepository(
        sceneUrl: 'https://example.invalid/scene.json',
      ),
      viewerFactory: (_) => FakeViewer(),
      sceneFactory: (_) => scene,
    );
    await tester.tap(find.text('自由視点で見る'));
    await tester.pump();
    await tester.tap(find.text('キャンセル'));
    await tester.pumpAndSettle();
    expect(scene.disposed, isTrue);
    expect(
      tester.widget<IconButton>(iconButton('次のハイライト')).onPressed,
      isNotNull,
    );
  });

  testWidgets(
    'free view keeps drawing on iframe focus but pauses when hidden',
    (tester) async {
      final scene = FakeViewer(surfaceKey: 'real-scene-surface');
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PreviewViewer(
              source: Uri.parse('https://example.invalid/scene.json'),
              factory: (_) => scene,
              freeView: true,
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
      expect(scene.pauseCount, 0);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      expect(scene.pauseCount, 1);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      expect(scene.resumeCount, 1);
    },
  );
}
