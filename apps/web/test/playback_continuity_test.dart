import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/clip_card.dart';

class HandoffViewer extends ViewerAdapter {
  HandoffViewer({
    this.scene = false,
    Duration position = Duration.zero,
    bool playing = false,
  }) : _value = ViewerValue(
         status: scene ? ViewerStatus.loading : ViewerStatus.ready,
         position: position,
         duration: const Duration(milliseconds: 800),
         playing: playing,
       );
  final bool scene;
  ViewerValue _value;
  PreviewSceneMapping? mapping;
  final requests = <PlaybackState>[];
  final completions = <Completer<bool>>[];
  final drawn = <int>[];
  bool disposed = false, paused = false;
  PlaybackState? captureOverride;
  int frames = 20;
  @override
  ViewerValue get value => _value;
  @override
  Future<void> initialize() async {}
  @override
  Future<PlaybackState> capturePlayback() async =>
      captureOverride ?? await super.capturePlayback();
  @override
  void configureContinuity(Uri preview, PreviewSceneMapping? declared) =>
      mapping = declared;
  ViewerTimeline get timeline =>
      ViewerTimeline(frameCount: frames, frameRate: 25, mapping: mapping);
  void prepared() {
    _value = ViewerValue(prepared: true, timeline: timeline);
    notifyListeners();
  }

  void emit(Duration position, bool playing) {
    _value = ViewerValue(
      status: ViewerStatus.ready,
      position: position,
      playing: playing,
      duration: scene
          ? const Duration(milliseconds: 760)
          : const Duration(milliseconds: 800),
      prepared: scene,
      canPlay: !scene || frames > 1,
      timeline: scene ? timeline : null,
    );
    notifyListeners();
  }

  @override
  Future<bool> restorePlayback(PlaybackState state) {
    requests.add(state);
    final completion = Completer<bool>();
    completions.add(completion);
    return completion.future;
  }

  void complete([int? index]) {
    final target = index ?? requests.length - 1;
    final request = requests[target];
    if (scene) drawn.add(timeline.frameAt(request.position));
    emit(request.position, request.playing);
    completions[target].complete(true);
  }

  void fail() {
    _value = const ViewerValue(status: ViewerStatus.error);
    notifyListeners();
  }

  @override
  Future<void> togglePlayback() async => emit(value.position, !value.playing);
  @override
  Future<void> seek(Duration position) async => emit(position, value.playing);
  @override
  Future<void> toggleMute() async {}
  @override
  Future<void> pause() async {
    paused = true;
  }

  @override
  Future<void> resume() async {
    paused = false;
  }

  @override
  Widget buildView() => ColoredBox(
    key: Key(scene ? 'handoff-scene' : 'handoff-video'),
    color: Colors.black,
  );
  @override
  void dispose() {
    disposed = true;
    for (final completion in completions) {
      if (!completion.isCompleted) completion.complete(false);
    }
    super.dispose();
  }
}

Clip clip({
  String id = 'game',
  String revision = 'r1',
  PreviewSceneMapping? mapping = const PreviewSceneMapping(),
}) => Clip(
  id: id,
  revision: revision,
  title: 'バスケットボール',
  sport: 'バスケットボール',
  description: '',
  previewUrl: Uri.parse('https://example.test/$id/$revision/preview.webm'),
  sceneUrl: Uri.parse('https://example.test/$id/$revision/scene.json'),
  playbackMapping: mapping,
);

Future<void> mount(
  WidgetTester tester,
  Clip source,
  List<HandoffViewer> videos,
  List<HandoffViewer> scenes, {
  bool active = true,
  bool initialFreeView = false,
  ValueChanged<bool>? changed,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  await tester.pumpWidget(
    MaterialApp(
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(context).copyWith(disableAnimations: true),
        child: child!,
      ),
      home: Scaffold(
        body: ClipCard(
          key: const ValueKey('active-slot'),
          clip: source,
          active: active,
          initialFreeView: initialFreeView,
          viewerFactory: (_) {
            final viewer = HandoffViewer(
              position: const Duration(milliseconds: 560),
            );
            videos.add(viewer);
            return viewer;
          },
          sceneFactory: (_) {
            final viewer = HandoffViewer(scene: true);
            scenes.add(viewer);
            return viewer;
          },
          onFreeViewChanged: changed ?? (_) {},
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> tap(WidgetTester tester, String label) async {
  await tester.tap(find.text(label));
  await tester.pumpAndSettle();
}

void main() {
  for (final frames in [1, 20]) {
    testWidgets(
      'direct $frames-frame result presents its first frame and retains later handoff time',
      (tester) async {
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
        await mount(
          tester,
          clip(id: 'generated-0123456789abcdef0123456789abcdef'),
          videos,
          scenes,
          initialFreeView: true,
        );
        expect(videos, isEmpty);
        final scene = scenes.single..frames = frames;
        scene.prepared();
        await tester.pumpAndSettle();
        expect(scene.requests, hasLength(1));
        expect(scene.requests.single.position, Duration.zero);
        expect(scene.requests.single.playing, isFalse);
        expect(scene.drawn, isEmpty);
        expect(find.text('自由視点を読み込んでいます'), findsOneWidget);
        scene.complete();
        await tester.pumpAndSettle();
        expect(scene.drawn, [0]);
        expect(find.text('自由視点を読み込んでいます'), findsNothing);

        final position = frames > 1
            ? const Duration(milliseconds: 400)
            : Duration.zero;
        scene.emit(position, frames > 1);
        await tap(tester, '動画に戻る');
        expect(videos.single.requests.single.position, position);
        expect(videos.single.requests.single.playing, frames > 1);
        videos.single.complete();
        await tester.pumpAndSettle();
        await tap(tester, '自由視点で見る');
        expect(scene.requests, hasLength(2));
        expect(scene.requests.last.position, position);
        scene.complete();
        await tester.pumpAndSettle();
        expect(scene.requests, hasLength(2));
        expect(scene.value.position, position);
        expect(scene.value.playing, frames > 1);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }

  test('anchors map seconds directly, round to source samples and clamp the last sample rather than normalize durations', () {
    const timeline = ViewerTimeline(frameCount: 20, frameRate: 25);
    expect(timeline.frameAt(const Duration(milliseconds: 560)), 14);
    expect(
      timeline.sample(const Duration(milliseconds: 800)),
      const Duration(milliseconds: 760),
    );
    const mapping = PreviewSceneMapping(previewStart: Duration(seconds: 2));
    expect(
      timeline.sample(mapping.toScene(const Duration(milliseconds: 2560))),
      const Duration(milliseconds: 560),
    );
    expect(
      mapping.toPreview(const Duration(milliseconds: 560)),
      const Duration(milliseconds: 2560),
    );
    expect(
      const ViewerTimeline(
        frameCount: 1,
        frameRate: 25,
      ).sample(const Duration(seconds: 20)),
      Duration.zero,
    );
  });
  testWidgets(
    'missing mapping starts scene at zero and preserves the original preview time and intent',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(mapping: null), videos, scenes);
      videos.single.emit(const Duration(milliseconds: 560), true);
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      expect(scenes.single.requests.single.position, Duration.zero);
      scenes.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.value.playing, isFalse);
      expect(find.textContaining('時刻の対応情報なし'), findsNothing);
      scenes.single.emit(const Duration(milliseconds: 400), false);
      await tap(tester, '動画に戻る');
      expect(
        videos.single.requests.single.position,
        const Duration(milliseconds: 560),
      );
      expect(videos.single.requests.single.playing, isTrue);
      videos.single.complete();
      await tester.pumpAndSettle();
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'captured native time selects sample14; return awaits video restoration without remounting',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(), videos, scenes);
      videos.single.captureOverride = const PlaybackState(
        Duration(milliseconds: 560),
        false,
      );
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      expect(
        scenes.single.requests.single.position,
        const Duration(milliseconds: 560),
      );
      expect(scenes.single.requests.single.playing, isFalse);
      expect(scenes.single.drawn, isEmpty);
      expect(find.text('動画に戻る'), findsNothing);
      scenes.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.drawn, [14]);
      expect(
        videos.single.disposed,
        isFalse,
        reason: 'immersive design retains the same native player',
      );
      expect(videos.single.paused, isTrue);
      scenes.single.emit(const Duration(milliseconds: 400), false);
      scenes.single.captureOverride = const PlaybackState(
        Duration(milliseconds: 760),
        false,
      );
      await tap(tester, '動画に戻る');
      expect(videos, hasLength(1));
      expect(
        videos.single.requests.single.position,
        const Duration(milliseconds: 760),
      );
      expect(videos.single.requests.single.playing, isFalse);
      expect(
        scenes.single.paused,
        isFalse,
        reason: 'GS stays visible until native restoration is acknowledged',
      );
      videos.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.paused, isTrue);
      expect(find.text('動画に戻る'), findsNothing);
      videos.single.captureOverride = const PlaybackState(
        Duration(milliseconds: 800),
        false,
      );
      await tap(tester, '自由視点で見る');
      expect(scenes, hasLength(1), reason: 'model and camera are retained');
      expect(
        scenes.single.requests.last.position,
        const Duration(milliseconds: 760),
      );
      scenes.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.drawn, [14, 19]);
      expect(find.textContaining('0.76秒へ調整'), findsNothing);
      await tester.pumpWidget(const SizedBox.shrink());
      expect(scenes.single.disposed, isTrue);
    },
  );

  testWidgets(
    'entry freezes a latched frame and restores the original play intent after reveal',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(), videos, scenes);
      videos.single.emit(const Duration(milliseconds: 560), true);
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      expect(
        videos.single.value.playing,
        isFalse,
        reason: 'video holds its frame during the transformation',
      );
      expect(
        scenes.single.requests.single.playing,
        isFalse,
        reason: 'scene cannot advance behind the reveal',
      );
      videos.single.emit(const Duration(milliseconds: 640), false);
      await tester.pumpAndSettle();
      expect(
        scenes.single.requests,
        hasLength(1),
        reason: 'progress cannot starve the first draw',
      );
      scenes.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.value.position, const Duration(milliseconds: 560));
      expect(scenes.single.value.playing, isTrue);
      scenes.single.emit(const Duration(milliseconds: 400), true);
      await tap(tester, '動画に戻る');
      expect(
        videos.single.requests.single.position,
        const Duration(milliseconds: 400),
      );
      expect(videos.single.requests.single.playing, isTrue);
      videos.single.complete();
      await tester.pumpAndSettle();
      expect(videos.single.value.playing, isTrue);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'cancel, failure and revision changes ignore old acknowledgements',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(), videos, scenes);
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      await tap(tester, 'キャンセル');
      expect(scenes.first.disposed, isTrue);
      expect(find.text('自由視点で見る'), findsOneWidget);
      videos.single.emit(const Duration(milliseconds: 200), false);
      await tap(tester, '自由視点で見る');
      scenes.last.prepared();
      await tester.pumpAndSettle();
      expect(
        scenes.last.requests.single.position,
        const Duration(milliseconds: 200),
      );
      scenes.last.fail();
      await tester.pumpAndSettle();
      expect(videos.single.disposed, isFalse);
      await tap(tester, '自由視点を再試行');
      scenes.last.prepared();
      await tester.pumpAndSettle();
      expect(
        scenes.last.requests.single.position,
        const Duration(milliseconds: 200),
      );
      await mount(tester, clip(revision: 'r2'), videos, scenes);
      expect(scenes.every((scene) => scene.disposed), isTrue);
      expect(videos.first.disposed, isTrue);
      expect(find.text('自由視点で見る'), findsOneWidget);
      expect(videos.last.requests, isEmpty);
      await mount(tester, clip(id: 'next'), videos, scenes);
      expect(videos.last.requests, isEmpty);
      await mount(tester, clip(id: 'next'), videos, scenes, active: false);
      expect(videos.every((video) => video.disposed), isTrue);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
