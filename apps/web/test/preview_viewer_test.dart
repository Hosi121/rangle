import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/preview_viewer.dart';
import 'package:rangle_web/widgets/edge_seek_bar.dart';

class ControlledViewer extends ViewerAdapter {
  ControlledViewer(this._value);

  ViewerValue _value;
  int toggleCount = 0;
  int surfaceTaps = 0;
  int pauseCount = 0;
  int resumeCount = 0;
  Duration? lastSeek;
  bool disposed = false;

  @override
  ViewerValue get value => _value;

  void update(ViewerValue value) {
    _value = value;
    notifyListeners();
  }

  @override
  Future<void> initialize() async {}
  @override
  Future<void> togglePlayback() async => toggleCount++;
  @override
  Future<void> seek(Duration position) async => lastSeek = position;
  @override
  Future<void> toggleMute() async {}
  @override
  Future<void> pause() async => pauseCount++;
  @override
  Future<void> resume() async => resumeCount++;

  @override
  Widget buildView() => GestureDetector(
    onTap: () => surfaceTaps++,
    child: const ColoredBox(key: Key('scene-surface'), color: Colors.black),
  );

  @override
  void dispose() {
    disposed = true;
    super.dispose();
  }
}

const playable = ViewerValue(
  status: ViewerStatus.ready,
  duration: Duration(milliseconds: 760),
);

Future<void> showViewer(
  WidgetTester tester,
  ControlledViewer viewer, {
  bool freeView = true,
  bool visible = true,
  VoidCallback? onReady,
  ValueChanged<ViewerStatus>? onStatusChanged,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: PreviewViewer(
          source: Uri.parse('https://example.invalid/scene.json'),
          factory: (_) => viewer,
          freeView: freeView,
          visible: visible,
          onReady: onReady,
          onStatusChanged: onStatusChanged,
        ),
      ),
    ),
  );
}

void main() {
  testWidgets(
    'short startup and loop buffering pulses leave the ready frame visible',
    (tester) async {
      final viewer = ControlledViewer(
        const ViewerValue(
          status: ViewerStatus.ready,
          duration: Duration(seconds: 20),
          playing: true,
          buffering: true,
        ),
      );
      await showViewer(tester, viewer, freeView: false);
      for (var i = 0; i < 4; i++) {
        await tester.pump(const Duration(milliseconds: 50));
        expect(find.text('映像を読み込んでいます'), findsNothing);
        expect(find.byKey(const Key('scene-surface')), findsOneWidget);
        expect(find.byType(ViewerMessage), findsNothing);
        viewer.update(
          const ViewerValue(
            status: ViewerStatus.ready,
            duration: Duration(seconds: 20),
            playing: true,
          ),
        );
        await tester.pump(const Duration(milliseconds: 700));
        expect(find.text('映像を読み込んでいます'), findsNothing);
        viewer.update(
          const ViewerValue(
            status: ViewerStatus.ready,
            duration: Duration(seconds: 20),
            playing: true,
            buffering: true,
          ),
        );
        await tester.pump();
      }
      await tester.pumpWidget(const SizedBox());
      await tester.pump(const Duration(seconds: 1));
      expect(viewer.disposed, isTrue);
    },
  );

  testWidgets(
    'sustained buffering survives value updates and clears on recovery or error',
    (tester) async {
      const waiting = ViewerValue(
        status: ViewerStatus.ready,
        duration: Duration(seconds: 20),
        playing: true,
        buffering: true,
      );
      final viewer = ControlledViewer(waiting);
      await showViewer(tester, viewer, freeView: false);
      await tester.pump(const Duration(milliseconds: 300));
      viewer.update(waiting);
      await tester.pump(const Duration(milliseconds: 299));
      expect(find.text('映像を読み込んでいます'), findsNothing);
      await tester.pump(const Duration(milliseconds: 1));
      expect(find.text('映像を読み込んでいます'), findsOneWidget);
      expect(find.byKey(const Key('scene-surface')), findsOneWidget);
      expect(find.byType(ViewerMessage), findsNothing);
      viewer.update(playable);
      await tester.pump();
      expect(find.text('映像を読み込んでいます'), findsNothing);
      viewer.update(waiting);
      await tester.pump(const Duration(milliseconds: 50));
      viewer.update(const ViewerValue(status: ViewerStatus.error));
      await tester.pump();
      expect(find.text('映像を再生できませんでした'), findsOneWidget);
      await tester.pump(const Duration(milliseconds: 600));
      expect(find.text('映像を読み込んでいます'), findsNothing);
    },
  );

  testWidgets('hiding or leaving a buffering clip cancels pending feedback', (
    tester,
  ) async {
    final viewer = ControlledViewer(
      const ViewerValue(
        status: ViewerStatus.ready,
        duration: Duration(seconds: 20),
        buffering: true,
      ),
    );
    await showViewer(tester, viewer, freeView: false);
    await tester.pump(const Duration(milliseconds: 300));
    await showViewer(tester, viewer, freeView: false, visible: false);
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('映像を読み込んでいます'), findsNothing);
    await showViewer(tester, viewer, freeView: false);
    await tester.pump(const Duration(milliseconds: 300));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    await tester.pump(const Duration(seconds: 1));
    expect(find.text('映像を読み込んでいます'), findsNothing);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('映像を読み込んでいます'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    await tester.pump(const Duration(seconds: 1));
    expect(viewer.disposed, isTrue);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'changing keyed clips releases the old viewer and resets stall timing',
    (tester) async {
      final viewers = <ControlledViewer>[];
      Future<void> clip(String id) => tester.pumpWidget(
        MaterialApp(
          home: PreviewViewer(
            key: ValueKey(id),
            source: Uri.parse('https://example.invalid/$id.webm'),
            factory: (_) {
              final viewer = ControlledViewer(
                const ViewerValue(
                  status: ViewerStatus.ready,
                  duration: Duration(seconds: 20),
                  buffering: true,
                ),
              );
              viewers.add(viewer);
              return viewer;
            },
          ),
        ),
      );
      await clip('first');
      await tester.pump(const Duration(milliseconds: 400));
      await clip('second');
      expect(viewers.first.disposed, isTrue);
      expect(viewers, hasLength(2));
      await tester.pump(const Duration(milliseconds: 200));
      expect(find.text('映像を読み込んでいます'), findsNothing);
      await tester.pump(const Duration(milliseconds: 400));
      expect(find.text('映像を読み込んでいます'), findsOneWidget);
      await tester.pumpWidget(const SizedBox());
      expect(viewers.last.disposed, isTrue);
    },
  );

  test('ordinary videos retain playback capability by default', () {
    expect(const ViewerValue().canPlay, isTrue);
  });

  testWidgets('free-view initial load waits for an actual ready frame', (
    tester,
  ) async {
    final viewer = ControlledViewer(const ViewerValue(canPlay: false));
    await showViewer(tester, viewer);
    expect(find.text('自由視点を読み込んでいます'), findsOneWidget);
    expect(find.byTooltip('再生'), findsNothing);
    expect(tester.widget<EdgeSeekBar>(find.byType(EdgeSeekBar)).onSeek, isNull);

    viewer.update(playable);
    await tester.pump();
    expect(find.text('自由視点を読み込んでいます'), findsNothing);
    expect(find.byIcon(Icons.play_arrow), findsNothing);
  });

  testWidgets(
    'GS playback has labeled keyboard controls and follows progress',
    (tester) async {
      final semantics = tester.ensureSemantics();
      try {
        final viewer = ControlledViewer(playable);
        await showViewer(tester, viewer);
        expect(find.byIcon(Icons.play_arrow), findsNothing);
        Focus.of(tester.element(find.byKey(const Key('scene-surface'))))
            .requestFocus();
        await tester.pump();
        await tester.sendKeyEvent(LogicalKeyboardKey.space);
        await tester.pump();
        expect(viewer.toggleCount, 1);

        viewer.update(
          const ViewerValue(
            status: ViewerStatus.ready,
            position: Duration(milliseconds: 400),
            duration: Duration(milliseconds: 760),
            playing: true,
          ),
        );
        await tester.pump();
        expect(find.byIcon(Icons.pause), findsNothing);
        expect(
          tester
              .getSemantics(find.byKey(const Key('media-scrubber')))
              .getSemanticsData()
              .value,
          '0.40秒 / 0.76秒',
        );
        expect(
          tester.widget<EdgeSeekBar>(find.byType(EdgeSeekBar)).position,
          const Duration(milliseconds: 400),
        );
        expect(find.byTooltip('ミュート'), findsNothing);
        expect(find.byIcon(Icons.touch_app_outlined), findsNothing);
      } finally {
        semantics.dispose();
      }
    },
  );

  testWidgets('GS buffering keeps orbit surface and seeking available', (
    tester,
  ) async {
    final viewer = ControlledViewer(playable);
    await showViewer(tester, viewer);
    viewer.update(
      const ViewerValue(
        status: ViewerStatus.ready,
        position: Duration(milliseconds: 360),
        duration: Duration(milliseconds: 760),
        playing: true,
        buffering: true,
      ),
    );
    await tester.pump();
    expect(find.byType(ViewerMessage), findsNothing);
    expect(find.text('時刻を読み込んでいます'), findsNothing);
    expect(find.byIcon(Icons.pause), findsNothing);
    await tester.tap(find.byKey(const Key('scene-surface')));
    expect(viewer.surfaceTaps, 1);
    tester.widget<EdgeSeekBar>(find.byType(EdgeSeekBar)).onSeek!(
      const Duration(milliseconds: 600),
    );
    expect(viewer.lastSeek, const Duration(milliseconds: 600));

    // A transient loading status must not cover a frame already displayed.
    viewer.update(const ViewerValue(canPlay: true));
    await tester.pump();
    expect(find.byType(ViewerMessage), findsNothing);
    await tester.tap(find.byKey(const Key('scene-surface')));
    expect(viewer.surfaceTaps, 2);
  });

  testWidgets('legacy GS scenes retain seeking without a play button', (
    tester,
  ) async {
    final viewer = ControlledViewer(
      const ViewerValue(
        status: ViewerStatus.ready,
        duration: Duration(milliseconds: 760),
        canPlay: false,
      ),
    );
    await showViewer(tester, viewer);
    expect(find.byTooltip('再生'), findsNothing);
    expect(find.byIcon(Icons.touch_app_outlined), findsNothing);
    expect(
      tester.widget<EdgeSeekBar>(find.byType(EdgeSeekBar)).onSeek,
      isNotNull,
    );
  });

  testWidgets('ordinary video keeps buffering feedback and mute controls', (
    tester,
  ) async {
    final viewer = ControlledViewer(
      const ViewerValue(
        status: ViewerStatus.ready,
        duration: Duration(seconds: 20),
        playing: true,
        buffering: true,
      ),
    );
    await showViewer(tester, viewer, freeView: false);
    expect(find.text('映像を読み込んでいます'), findsNothing);
    await tester.pump(const Duration(milliseconds: 600));
    expect(find.text('映像を読み込んでいます'), findsOneWidget);
    expect(find.byIcon(Icons.pause), findsNothing);
    expect(find.byTooltip('ミュート'), findsOneWidget);
  });

  testWidgets('GS errors remain visible after the first frame', (tester) async {
    final viewer = ControlledViewer(playable);
    await showViewer(tester, viewer);
    viewer.update(
      const ViewerValue(status: ViewerStatus.error, canPlay: false),
    );
    await tester.pump();
    expect(find.text('自由視点を表示できませんでした'), findsOneWidget);
    expect(find.text('再試行'), findsOneWidget);
  });

  testWidgets(
    'retained hidden GS waits for readiness and stays paused on app resume',
    (tester) async {
      final viewer = ControlledViewer(const ViewerValue(canPlay: false));
      final statuses = <ViewerStatus>[];
      var readyCount = 0;
      await showViewer(
        tester,
        viewer,
        visible: false,
        onReady: () => readyCount++,
        onStatusChanged: statuses.add,
      );
      expect(viewer.pauseCount, 1);
      expect(viewer.resumeCount, 0);
      expect(readyCount, 0);
      expect(statuses, [ViewerStatus.loading]);
      viewer.update(playable);
      await tester.pump();
      expect(readyCount, 1);
      expect(statuses, [ViewerStatus.loading, ViewerStatus.ready]);
      expect(viewer.resumeCount, 0);
      await showViewer(tester, viewer, onReady: () => readyCount++);
      expect(viewer.resumeCount, 1);
      viewer.update(playable);
      await tester.pump();
      expect(readyCount, 1);
      await showViewer(tester, viewer, visible: false);
      expect(viewer.pauseCount, 2);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
      tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
      expect(viewer.resumeCount, 1);
      expect(viewer.disposed, isFalse);
    },
  );
}
