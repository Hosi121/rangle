import 'dart:async';

import 'package:flutter/cupertino.dart' show CupertinoSlider;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/preview_viewer.dart';

class TestRepository implements ClipRepository {
  TestRepository(this.loadClips);
  final Future<List<Clip>> Function() loadClips;

  @override
  Future<List<Clip>> load() => loadClips();
}

class FakeViewer extends ViewerAdapter {
  FakeViewer({
    ViewerStatus initialStatus = ViewerStatus.ready,
    bool canPlay = true,
    this.surfaceKey = 'real-video-surface',
  }) : _value = ViewerValue(
         status: initialStatus,
         duration: const Duration(seconds: 20),
         canPlay: canPlay,
       );

  ViewerValue _value;
  final String surfaceKey;
  bool disposed = false;
  bool paused = false;
  int pauseCount = 0;
  int resumeCount = 0;
  Duration? lastSeek;

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {}

  void fail() {
    _value = const ViewerValue(status: ViewerStatus.error);
    notifyListeners();
  }

  void becomeReady() {
    _value = const ViewerValue(
      status: ViewerStatus.ready,
      duration: Duration(seconds: 20),
    );
    notifyListeners();
  }

  @override
  Future<void> togglePlayback() async {
    _value = ViewerValue(
      status: _value.status,
      playing: !_value.playing,
      canPlay: _value.canPlay,
      muted: _value.muted,
      position: _value.position,
      duration: _value.duration,
    );
    notifyListeners();
  }

  @override
  Future<void> seek(Duration position) async {
    lastSeek = position;
    _value = ViewerValue(
      status: _value.status,
      position: position,
      duration: _value.duration,
      playing: _value.playing,
      canPlay: _value.canPlay,
      muted: _value.muted,
    );
    notifyListeners();
  }

  @override
  Future<void> toggleMute() async {
    _value = ViewerValue(
      status: _value.status,
      position: _value.position,
      duration: _value.duration,
      playing: _value.playing,
      canPlay: _value.canPlay,
      muted: !_value.muted,
    );
    notifyListeners();
  }

  @override
  Future<void> pause() async {
    pauseCount++;
    paused = true;
  }

  @override
  Future<void> resume() async {
    resumeCount++;
    paused = false;
  }

  @override
  Widget buildView() => ColoredBox(key: Key(surfaceKey), color: Colors.black);

  @override
  void dispose() {
    disposed = true;
    paused = true;
    pauseCount++;
    super.dispose();
  }
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

void main() {
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
    final play = tester.widget<IconButton>(iconButton('映像準備中のため再生できません'));
    expect(play.onPressed, isNull);
    expect(tester.widget<Slider>(find.byType(Slider)).onChanged, isNull);
    final freeView = find.ancestor(
      of: find.text('自由視点は準備中'),
      matching: find.byType(FilledButton),
    );
    expect(tester.widget<FilledButton>(freeView).onPressed, isNull);
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
      expect(player.contains(tester.getCenter(iconButton('再生'))), isTrue);
      expect(
        player.contains(tester.getCenter(find.text('VRU Basketball'))),
        isTrue,
      );
      expect(find.textContaining('CC BY 4.0'), findsNothing);
      await tester.tap(iconButton('出典・詳細'));
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
      await tester.tap(iconButton('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('real-scene-surface')), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.tap(iconButton('出典・詳細'));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.textContaining('CC BY 4.0'));
      expect(find.textContaining('CC BY 4.0'), findsOneWidget);
      await tester.tap(iconButton('閉じる'));
      await tester.pumpAndSettle();
      await tester.tap(iconButton('動画に戻る'));
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
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      expect(find.text('03 / 03'), findsOneWidget);
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
    await tester.tap(iconButton('再生'));
    await tester.pump();
    expect(viewers.single.value.playing, isTrue);
    expect(iconButton('一時停止'), findsOneWidget);
    await tester.tap(iconButton('ミュート'));
    await tester.pump();
    expect(viewers.single.value.muted, isTrue);
    await tester.tap(find.byKey(const Key('media-scrubber')));
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
    expect(tester.widget<IconButton>(iconButton('再生')).onPressed, isNull);
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
    'legacy free view seeks without playback and releases on return or navigation',
    (tester) async {
      final videos = <FakeViewer>[];
      final scenes = <FakeViewer>[];
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) {
          final viewer = FakeViewer();
          videos.add(viewer);
          return viewer;
        },
        sceneFactory: (_) {
          final viewer = FakeViewer(
            surfaceKey: 'real-scene-surface',
            canPlay: false,
          );
          scenes.add(viewer);
          return viewer;
        },
      );
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(videos.single.disposed, isTrue);
      expect(find.byKey(const Key('real-video-surface')), findsNothing);
      expect(find.byKey(const Key('real-scene-surface')), findsOneWidget);
      expect(iconButton('再生'), findsNothing);
      expect(
        tester
            .widget<PageView>(find.byKey(const Key('highlight-feed')))
            .physics,
        isA<NeverScrollableScrollPhysics>(),
      );
      final scrubber = tester.getRect(find.byKey(const Key('media-scrubber')));
      await tester.dragFrom(
        Offset(scrubber.left + 18, scrubber.center.dy),
        Offset(scrubber.width / 2, 0),
      );
      await tester.pump();
      expect(scenes.single.lastSeek, isNotNull);
      expect(
        tester
            .widget<CupertinoSlider>(find.byKey(const Key('media-scrubber')))
            .onChanged,
        isNotNull,
      );
      await tester.tap(find.text('動画に戻る'));
      await tester.pumpAndSettle();
      expect(scenes.single.disposed, isFalse);
      expect(scenes.single.paused, isTrue);
      expect(videos, hasLength(2));
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(scenes, hasLength(1));
      expect(scenes.single.paused, isFalse);
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      expect(scenes.last.disposed, isTrue);
      expect(find.byKey(const Key('real-scene-surface')), findsNothing);
    },
  );

  testWidgets(
    'free-view failure retries with a new viewer and keeps phone layout usable',
    (tester) async {
      final scenes = <FakeViewer>[];
      await pumpApp(
        tester,
        size: const Size(844, 390),
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) => FakeViewer(),
        sceneFactory: (_) {
          final viewer = FakeViewer(surfaceKey: 'real-scene-surface');
          scenes.add(viewer);
          return viewer;
        },
      );
      await tester.tap(iconButton('自由視点で見る'));
      await tester.pumpAndSettle();
      scenes.single.fail();
      await tester.pumpAndSettle();
      expect(find.textContaining('自由視点を表示できませんでした'), findsOneWidget);
      expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
      await tester.tap(iconButton('自由視点を再試行'));
      await tester.pumpAndSettle();
      expect(scenes.first.disposed, isTrue);
      expect(scenes, hasLength(2));
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'cancel disposes pending GS while completed GS is retained for reentry',
    (tester) async {
      final videos = <FakeViewer>[];
      final scenes = <FakeViewer>[];
      await pumpApp(
        tester,
        repository: const DemoClipRepository(
          sceneUrl: 'https://example.invalid/scene.json',
        ),
        viewerFactory: (_) {
          final viewer = FakeViewer();
          videos.add(viewer);
          return viewer;
        },
        sceneFactory: (_) {
          final viewer = FakeViewer(
            initialStatus: ViewerStatus.loading,
            canPlay: false,
            surfaceKey: 'real-scene-surface',
          );
          scenes.add(viewer);
          return viewer;
        },
      );
      await tester.tap(iconButton('再生'));
      await tester.pump();
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(videos, hasLength(1));
      expect(videos.single.disposed, isFalse);
      expect(videos.single.value.playing, isTrue);
      expect(scenes.single.paused, isTrue);
      final sceneWidget = tester.widget<PreviewViewer>(
        find.byWidgetPredicate(
          (widget) => widget is PreviewViewer && widget.freeView,
        ),
      );
      expect(sceneWidget.visible, isFalse);
      await tester.tap(iconButton('一時停止'));
      await tester.pump();
      expect(videos.single.value.playing, isFalse);
      await tester.tap(find.byType(Slider));
      await tester.pump();
      expect(videos.single.lastSeek, isNotNull);

      await tester.tap(find.text('自由視点の準備をキャンセル'));
      // Queue a late ready callback before the cancelled widget is removed.
      scenes.single.becomeReady();
      await tester.pumpAndSettle();
      expect(videos.single.disposed, isFalse);
      expect(scenes.single.disposed, isTrue);
      expect(scenes.single.paused, isTrue);
      expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
      expect(find.byKey(const Key('real-scene-surface')), findsNothing);
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(scenes, hasLength(2));
      expect(videos.single.disposed, isFalse);
      expect(scenes.last.paused, isTrue);
      scenes.last.becomeReady();
      await tester.pumpAndSettle();
      expect(videos.single.disposed, isTrue);
      expect(scenes.last.paused, isFalse);
      await tester.tap(find.text('動画に戻る'));
      await tester.pumpAndSettle();
      expect(scenes.last.paused, isTrue);
      expect(scenes.last.disposed, isFalse);
      expect(videos, hasLength(2));
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      expect(scenes, hasLength(2));
      expect(scenes.last.paused, isFalse);
      await tester.tap(iconButton('次のハイライト'));
      await tester.pumpAndSettle();
      expect(scenes.last.disposed, isTrue);
    },
  );

  testWidgets('navigation while GS prepares releases the pending scene', (
    tester,
  ) async {
    final video = FakeViewer();
    final scene = FakeViewer(
      initialStatus: ViewerStatus.loading,
      canPlay: false,
      surfaceKey: 'real-scene-surface',
    );
    await pumpApp(
      tester,
      repository: const DemoClipRepository(
        sceneUrl: 'https://example.invalid/scene.json',
      ),
      viewerFactory: (_) => video,
      sceneFactory: (_) => scene,
    );
    await tester.tap(find.text('自由視点で見る'));
    await tester.pumpAndSettle();
    expect(video.disposed, isFalse);
    expect(scene.paused, isTrue);
    await tester.tap(iconButton('次のハイライト'));
    await tester.pumpAndSettle();
    expect(video.disposed, isTrue);
    expect(scene.disposed, isTrue);
  });

  testWidgets(
    'failed GS preparation retains video and retry replaces the scene',
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
          final scene = FakeViewer(
            initialStatus: ViewerStatus.loading,
            canPlay: false,
            surfaceKey: 'real-scene-surface',
          );
          scenes.add(scene);
          return scene;
        },
      );
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      scenes.single.fail();
      await tester.pumpAndSettle();
      expect(scenes.single.disposed, isTrue);
      expect(video.disposed, isFalse);
      expect(find.textContaining('自由視点を表示できませんでした'), findsOneWidget);
      await tester.tap(iconButton('再生'));
      await tester.pump();
      expect(video.value.playing, isTrue);
      await tester.tap(find.text('自由視点を再試行'));
      await tester.pumpAndSettle();
      expect(scenes, hasLength(2));
      expect(video.disposed, isFalse);
      scenes.last.becomeReady();
      await tester.pumpAndSettle();
      expect(video.disposed, isTrue);
      expect(scenes.last.paused, isFalse);
    },
  );

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
