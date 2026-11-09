import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/clip_card.dart';
import 'package:rangle_web/widgets/edge_seek_bar.dart';

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
  int frames = 20;
  @override
  ViewerValue get value => _value;
  @override
  Future<void> initialize() async {}
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

Clip clip({String id = 'game', String revision = 'r1'}) => Clip(
  id: id,
  revision: revision,
  title: 'バスケットボール',
  sport: 'バスケットボール',
  description: '',
  previewUrl: Uri.parse('https://example.test/$id/$revision/preview.webm'),
  sceneUrl: Uri.parse('https://example.test/$id/$revision/scene.json'),
  playbackMapping: const PreviewSceneMapping(),
);

Future<void> mount(
  WidgetTester tester,
  Clip source,
  List<HandoffViewer> videos,
  List<HandoffViewer> scenes, {
  bool active = true,
  ValueChanged<bool>? changed,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: ClipCard(
          key: const ValueKey('active-slot'),
          clip: source,
          active: active,
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
    'focused .56 paused → first GS sample14 → sample19 → paused video, with explicit .80 end clamp',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(), videos, scenes);
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      expect(
        scenes.single.requests.single.position,
        const Duration(milliseconds: 560),
      );
      expect(scenes.single.requests.single.playing, isFalse);
      expect(scenes.single.drawn, isEmpty);
      expect(videos.single.disposed, isFalse);
      scenes.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.drawn, [14]);
      expect(videos.single.disposed, isTrue);
      scenes.single.emit(const Duration(milliseconds: 760), false);
      await tester.pumpAndSettle();
      await tap(tester, '動画に戻る');
      expect(videos, hasLength(2));
      expect(
        videos.last.requests.single.position,
        const Duration(milliseconds: 760),
      );
      expect(videos.last.requests.single.playing, isFalse);
      expect(
        scenes.single.paused,
        isFalse,
        reason: 'old scene remains available during native player restoration',
      );
      videos.last.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.paused, isTrue);
      videos.last.emit(const Duration(milliseconds: 800), false);
      await tester.pumpAndSettle();
      await tap(tester, '自由視点で見る');
      expect(
        scenes,
        hasLength(1),
        reason: 'cached model and camera are retained',
      );
      expect(
        scenes.single.requests.last.position,
        const Duration(milliseconds: 760),
      );
      scenes.single.complete();
      await tester.pumpAndSettle();
      expect(scenes.single.drawn, [14, 19]);
      expect(find.textContaining('0.76秒へ調整'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
      expect(scenes.single.disposed, isTrue);
    },
  );

  testWidgets(
    'video remains usable during preparation and only the latest requested time and play intent reveal GS',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(), videos, scenes);
      videos.single.emit(const Duration(milliseconds: 560), true);
      await tester.pumpAndSettle();
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      videos.single.emit(const Duration(milliseconds: 640), true);
      await tester.pumpAndSettle();
      expect(
        scenes.single.requests,
        hasLength(1),
        reason: 'automatic progress cannot indefinitely supersede a slow target draw',
      );
      tester.widget<EdgeSeekBar>(find.byType(EdgeSeekBar).last).onSeek!(
        const Duration(milliseconds: 720),
      );
      await tester.pumpAndSettle();
      expect(scenes.single.requests, hasLength(2));
      expect(
        scenes.single.requests.last.position,
        const Duration(milliseconds: 720),
      );
      await tester.tap(find.byKey(const Key('video-playback-surface')));
      await tester.pumpAndSettle();
      expect(scenes.single.requests, hasLength(3));
      scenes.single.complete(0);
      await tester.pumpAndSettle();
      expect(videos.single.disposed, isFalse);
      expect(find.text('自由視点の準備をキャンセル'), findsOneWidget);
      scenes.single.complete(2);
      await tester.pumpAndSettle();
      expect(videos.single.disposed, isTrue);
      expect(scenes.single.value.position, const Duration(milliseconds: 720));
      expect(scenes.single.value.playing, isFalse);
      scenes.single.emit(const Duration(milliseconds: 400), true);
      await tester.pumpAndSettle();
      await tap(tester, '動画に戻る');
      expect(
        videos.last.requests.single.position,
        const Duration(milliseconds: 400),
      );
      expect(videos.last.requests.single.playing, isTrue);
      videos.last.complete();
      await tester.pumpAndSettle();
      expect(videos.last.value.playing, isTrue);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'cancel, retry and revision changes cannot resurrect stale playback acknowledgements',
    (tester) async {
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final videos = <HandoffViewer>[], scenes = <HandoffViewer>[];
      await mount(tester, clip(), videos, scenes);
      await tap(tester, '自由視点で見る');
      scenes.single.prepared();
      await tester.pumpAndSettle();
      await tap(tester, '自由視点の準備をキャンセル');
      expect(scenes.first.disposed, isTrue);
      videos.single.emit(const Duration(milliseconds: 200), false);
      await tester.pumpAndSettle();
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
      expect(find.text('自由視点で見る'), findsOneWidget);
      expect(
        videos.last.requests,
        isEmpty,
        reason:
            'new revision uses fresh VOD autoplay instead of stale restoration',
      );
      await mount(tester, clip(id: 'next'), videos, scenes);
      expect(videos.last.requests, isEmpty);
      await mount(tester, clip(id: 'next'), videos, scenes, active: false);
      expect(videos.every((video) => video.disposed), isTrue);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
