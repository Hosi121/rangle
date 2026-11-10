import 'dart:convert';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/live/live_api.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/widgets/rangle_header.dart';

import 'live_test.dart' as live_fixture;
import 'support/fake_viewer.dart';
import 'support/generation_fixture.dart';

void main() {
  setUpAll(() async {
    TestWidgetsFlutterBinding.ensureInitialized();
    for (final e in {
      'Roboto': const String.fromEnvironment('RANGLE_CAPTURE_FONT'),
      'MaterialIcons': const String.fromEnvironment('RANGLE_CAPTURE_ICONS'),
    }.entries) {
      if (e.value.isEmpty) continue;
      await (FontLoader(e.key)
            ..addFont(File(e.value).readAsBytes().then(ByteData.sublistView)))
          .load();
    }
  });
  for (final size in [
    const Size(320, 640),
    const Size(390, 844),
    const Size(844, 390),
    const Size(1440, 900),
  ]) {
    testWidgets(
      'LIVE and generation keep brand, video space and working controls at $size',
      (tester) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        final generation = GenerationFixture(admin: false)..mode = 'live';
        final viewers = <live_fixture.UpdatingViewer>[];
        final now = DateTime.now().toUtc();
        final client = MockClient((request) async {
          final id = request.url.path == '/api/v1/live'
              ? 'game'
              : request.url.path.split('/').last;
          final data = live_fixture.liveSession(sequence: id == 'game' ? 1 : 0)
            ..['id'] = id
            ..['started_at'] = now
                .subtract(const Duration(seconds: 30))
                .toIso8601String()
            ..['updated_at'] = now.toIso8601String();
          if (id == 'game') {
            final segment =
                (data['segments'] as List).first as Map<String, Object?>;
            segment['captured_at'] = now
                .subtract(const Duration(seconds: 12))
                .toIso8601String();
            segment['available_at'] = now
                .subtract(const Duration(seconds: 1))
                .toIso8601String();
          }
          return http.Response(
            jsonEncode(
              request.url.path == '/api/v1/live'
                  ? {
                      'version': 1,
                      'sessions': [data],
                    }
                  : data,
            ),
            200,
            headers: {'content-type': 'application/json; charset=utf-8'},
          );
        });
        final key = GlobalKey();
        Future<void> showApp({bool initialGeneration = false}) =>
            tester.pumpWidget(
              RepaintBoundary(
                key: key,
                child: RangleApp(
                  key: ValueKey(initialGeneration),
                  initialGeneration: initialGeneration,
                  repository: const DemoClipRepository(),
                  community: generation.community,
                  generation: generation.api,
                  live: LiveApi(
                    endpoint: Uri.parse('https://app.example.test/api/v1/live'),
                    client: client,
                  ),
                  viewerFactory: (_) => FakeViewer(),
                  sceneFactory: (_) => FakeViewer(),
                  liveFactory: (source) {
                    final viewer = live_fixture.UpdatingViewer(source);
                    viewers.add(viewer);
                    return viewer;
                  },
                ),
              ),
            );
        await showApp();
        await tester.pumpAndSettle();
        Future<void> capture(String stage) async {
          const dir = String.fromEnvironment('RANGLE_CAPTURE_DIR');
          if (dir.isEmpty) return;
          await tester.runAsync(() async {
            final image =
                await (key.currentContext!.findRenderObject()
                        as RenderRepaintBoundary)
                    .toImage();
            final bytes = await image.toByteData(
              format: ui.ImageByteFormat.png,
            );
            image.dispose();
            await Directory(dir).create(recursive: true);
            await File('$dir/live-${size.width.toInt()}-$stage.png')
                .writeAsBytes(bytes!.buffer.asUint8List());
          });
        }

        await capture('feed');
        final feedWidth = tester
            .getSize(find.byKey(const Key('feed-player')))
            .width;
        await tester.tap(find.byTooltip('LIVEを開く'));
        await tester.pumpAndSettle();
        await capture('list');
        await tester.tap(find.byType(ListTile));
        await tester.pumpAndSettle();
        // Navigation reserves its own 64px instead of overlapping the viewer.
        expect(
          tester.getSize(find.byKey(const Key('live-surface'))),
          Size(feedWidth, size.height - 64),
        );
        expect(
          tester.getRect(find.byType(RangleHeader)).bottom,
          tester.getRect(find.byKey(const Key('live-surface'))).top,
        );
        expect(tester.getSize(find.byType(RangleHeader)).height, 64);
        for (final element in find.byType(PointerInterceptor).evaluate()) {
          expect(
            tester
                .getRect(find.byWidget(element.widget))
                .contains(Offset(size.width / 2, size.height / 2)),
            isFalse,
          );
        }
        await capture('viewer');
        await tester.tap(find.byTooltip('一時停止'));
        await tester.pump();
        expect(viewers.single.value.playing, isFalse);
        await tester.tap(find.byTooltip('再生'));
        await tester.pump();
        expect(viewers.single.value.playing, isTrue);
        await tester.tap(find.byTooltip('配信の出典'));
        await tester.pumpAndSettle();
        expect(
          find.ancestor(
            of: find.byType(ModalBarrier),
            matching: find.byType(PointerInterceptor),
          ),
          findsOneWidget,
        );
        await tester.tap(find.text('閉じる'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('フィードに戻る'));
        await tester.pumpAndSettle();
        expect(viewers.single.disposed, isTrue);
        generation.admin = true;
        generation.user!['is_admin'] = true;
        await showApp(initialGeneration: true);
        await tester.pumpAndSettle();
        await capture('generation');
        await tester.ensureVisible(find.text('LIVEを開始'));
        await tester.tap(find.text('LIVEを開始'));
        await tester.pumpAndSettle();
        expect(generation.creates, hasLength(1));
        await tester.ensureVisible(find.text('LIVEを見る'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('LIVEを見る'));
        await tester.pumpAndSettle();
        await capture('owner');
        await tester.tap(find.text('生成に戻る'));
        await tester.pumpAndSettle();
        await tester.scrollUntilVisible(
          find.text('LIVEを見る'),
          200,
          scrollable: find.byType(Scrollable).first,
        );
        await tester.ensureVisible(find.text('LIVEを見る'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('LIVEを見る'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('LIVEを停止'));
        await tester.pumpAndSettle();
        await tester.tap(find.text('中止する'));
        await tester.pumpAndSettle();
        expect(generation.deletes, 1);
        expect(tester.takeException(), isNull);
        await tester.pumpWidget(const SizedBox());
        client.close();
        generation.client.close();
      },
    );
  }
}
