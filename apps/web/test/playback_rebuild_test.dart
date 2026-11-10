import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/community/clip_actions.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/cheer_seek_bar.dart';
import 'package:rangle_web/widgets/clip_card.dart';

class _Viewer extends ViewerAdapter {
  ViewerValue current = const ViewerValue(
    status: ViewerStatus.ready,
    duration: Duration(seconds: 20),
  );
  var surfaceBuilds = 0;
  @override
  ViewerValue get value => current;
  void emit(ViewerValue next) {
    current = next;
    notifyListeners();
  }

  @override
  Future<void> initialize() async {}
  @override
  Future<void> togglePlayback() async => emit(
    ViewerValue(
      status: current.status,
      duration: current.duration,
      position: current.position,
      playing: !current.playing,
    ),
  );
  @override
  Future<void> seek(Duration position) async => emit(
    ViewerValue(
      status: current.status,
      duration: current.duration,
      position: position,
      playing: current.playing,
    ),
  );
  @override
  Future<void> toggleMute() async {}
  @override
  Future<void> pause() async {}
  @override
  Widget buildView() {
    surfaceBuilds++;
    return const ColoredBox(color: Colors.black);
  }
}

void main() {
  testWidgets(
    'playhead updates reach seeking without rebuilding the media surface or actions',
    (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final viewer = _Viewer();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ClipCard(
              clip: Clip(
                id: 'game',
                title: 'game',
                sport: 'basketball',
                description: '',
                sceneUrl: Uri.parse('https://example.invalid/scene.json'),
              ),
              active: true,
              initialFreeView: true,
              viewerFactory: (_) => throw StateError('No video expected'),
              sceneFactory: (_) => viewer,
              onFreeViewChanged: (_) {},
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      final surfaceBuilds = viewer.surfaceBuilds;
      final actions = tester.widget<ClipActions>(find.byType(ClipActions));
      final seekBar = tester.widget<CheerSeekBar>(find.byType(CheerSeekBar));
      for (var i = 0; i < 60; i++) {
        viewer.emit(
          const ViewerValue(
            status: ViewerStatus.ready,
            duration: Duration(seconds: 20),
          ),
        );
        await tester.pump(const Duration(milliseconds: 16));
      }
      expect(viewer.surfaceBuilds, surfaceBuilds);
      expect(
        identical(
          tester.widget<CheerSeekBar>(find.byType(CheerSeekBar)),
          seekBar,
        ),
        isTrue,
      );
      for (var i = 1; i <= 60; i++) {
        await viewer.seek(Duration(milliseconds: i * 16));
        await tester.pump(const Duration(milliseconds: 16));
      }
      expect(viewer.surfaceBuilds, surfaceBuilds);
      expect(
        identical(
          tester.widget<ClipActions>(find.byType(ClipActions)),
          actions,
        ),
        isTrue,
      );
      expect(
        tester.widget<CheerSeekBar>(find.byType(CheerSeekBar)).position,
        const Duration(milliseconds: 960),
      );
      expect(find.byTooltip('再生'), findsOneWidget);
      await viewer.togglePlayback();
      await tester.pump();
      expect(find.byTooltip('一時停止'), findsOneWidget);
      expect(viewer.surfaceBuilds, greaterThan(surfaceBuilds));
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    },
  );
}
