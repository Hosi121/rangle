import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/brand_art.dart';
import 'package:rangle_web/widgets/immersive_effect.dart';

import 'support/fake_viewer.dart';

void main() {
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    for (final entry in {
      'Roboto': const String.fromEnvironment('RANGLE_CAPTURE_FONT'),
      'MaterialIcons': const String.fromEnvironment('RANGLE_CAPTURE_ICONS'),
    }.entries) {
      if (entry.value.isEmpty) continue;
      final loader = FontLoader(entry.key)
        ..addFont(
          File(entry.value).readAsBytes().then((b) => ByteData.sublistView(b)),
        );
      await loader.load();
    }
  });
  test('pressure zoom stays magnified through the complete reveal', () {
    expect(entryZoom(0, 0), 1);
    expect(entryZoom(.5, 0), greaterThan(1));
    expect(entryZoom(1, 0), 1.75);
    expect(entryZoom(1, 2000 / 2850), 1.75);
    expect(entryZoom(1, 1), 1.75);
  });
  for (final size in [const Size(390, 844), const Size(1440, 900)]) {
    testWidgets('entry renders and exposes controls at $size', (tester) async {
      tester.view.physicalSize = size;
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final key = GlobalKey();
      final scene = FakeViewer(
        initialStatus: ViewerStatus.loading,
        surfaceKey: 'scene-layout',
      );
      await tester.pumpWidget(
        RepaintBoundary(
          key: key,
          child: RangleApp(
            repository: const DemoClipRepository(
              sceneUrl: 'https://example.invalid/scene.json',
            ),
            viewerFactory: (_) => FakeViewer(),
            sceneFactory: (_) => scene,
          ),
        ),
      );
      await tester.pumpAndSettle();
      Future<void> capture(String stage) async {
        const directory = String.fromEnvironment('RANGLE_CAPTURE_DIR');
        if (directory.isEmpty) return;
        await tester.runAsync(() async {
          final image =
              await (key.currentContext!.findRenderObject()
                      as RenderRepaintBoundary)
                  .toImage();
          final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
          image.dispose();
          await Directory(directory).create(recursive: true);
          await File('$directory/flutter-${size.width.toInt()}-$stage.png')
              .writeAsBytes(bytes!.buffer.asUint8List());
        });
      }

      await capture('video');
      final gesture = await tester.startGesture(
        tester.getCenter(find.byKey(const Key('immersive-hold-area'))),
      );
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump(const Duration(milliseconds: 1000));
      await capture('charge');
      await tester.pump(const Duration(milliseconds: 300));
      await tester.pump();
      expect(tester.widget<FlameMark>(find.byType(FlameMark)).eye, 0);
      scene.becomeReady();
      await tester.pump();
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 100));
      final mark = tester.widget<FlameMark>(find.byType(FlameMark));
      expect(mark.eye, greaterThan(0));
      expect(mark.orbit, greaterThan(0));
      await tester.pump(const Duration(milliseconds: 500));
      await capture('awakened');
      await tester.pumpAndSettle();
      expect(find.text('動画に戻る'), findsOneWidget);
      final sceneScale = tester.widget<Transform>(
        find
            .ancestor(
              of: find.byKey(const Key('entry-scene-content')),
              matching: find.byType(Transform),
            )
            .first,
      );
      expect(sceneScale.transform.entry(0, 0), 1.75);
      await gesture.up();
      await tester.pump();
      expect(find.text('動画に戻る'), findsOneWidget);
      await capture('freeview');
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    });
  }
}
