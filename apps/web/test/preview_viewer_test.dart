import 'package:flutter/cupertino.dart' show CupertinoSlider;
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/preview_viewer.dart';

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
    expect(
      tester.widget<CupertinoSlider>(find.byType(CupertinoSlider)).onChanged,
      isNull,
    );

    viewer.update(playable);
    await tester.pump();
    expect(find.text('自由視点を読み込んでいます'), findsNothing);
    expect(find.byTooltip('再生'), findsOneWidget);
  });

  testWidgets(
    'GS playback has labeled keyboard controls and follows progress',
    (tester) async {
      final semantics = tester.ensureSemantics();
      try {
        final viewer = ControlledViewer(playable);
        await showViewer(tester, viewer);
        final play = find.byTooltip('再生');
        expect(
          tester.getSemantics(play),
          matchesSemantics(
            label: '',
            tooltip: '再生',
            isButton: true,
            hasEnabledState: true,
            isEnabled: true,
            isFocusable: true,
            hasTapAction: true,
            hasFocusAction: true,
          ),
        );
        // Exercise the actual Material button's Enter binding.
        Focus.of(tester.element(find.byIcon(Icons.play_arrow))).requestFocus();
        await tester.pump();
        await tester.sendKeyEvent(LogicalKeyboardKey.enter);
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
        expect(find.byTooltip('一時停止'), findsOneWidget);
        expect(find.text('0.40秒 / 0.76秒'), findsOneWidget);
        expect(
          tester.widget<CupertinoSlider>(find.byType(CupertinoSlider)).value,
          400,
        );
        expect(find.byTooltip('ミュート'), findsNothing);
        expect(find.byTooltip('ドラッグで視点操作、ダブルタップで再生・停止'), findsOneWidget);
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
    expect(find.byTooltip('一時停止'), findsOneWidget);
    await tester.tap(find.byKey(const Key('scene-surface')));
    expect(viewer.surfaceTaps, 1);
    tester.widget<CupertinoSlider>(find.byType(CupertinoSlider)).onChanged!(
      600,
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
    expect(find.byTooltip('ドラッグで視点操作'), findsOneWidget);
    expect(
      tester.widget<CupertinoSlider>(find.byType(CupertinoSlider)).onChanged,
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
    expect(find.text('映像を読み込んでいます'), findsOneWidget);
    expect(find.byTooltip('一時停止'), findsOneWidget);
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
