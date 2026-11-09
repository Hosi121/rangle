import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/data/api_clip_repository.dart';
import 'package:rangle_web/generation/generation_screen.dart';
import 'package:rangle_web/main.dart';

import 'support/fake_viewer.dart';
import 'support/generation_fixture.dart';

Future<void> tapText(WidgetTester tester, String label) async {
  final target = find.text(label);
  if (target.evaluate().isEmpty) {
    await tester.scrollUntilVisible(
      target,
      200,
      scrollable: find.byType(Scrollable).first,
    );
  }
  await tester.ensureVisible(target);
  await tester.tap(target);
  await tester.pumpAndSettle();
}

void main() {
  testWidgets(
    'publication changes revalidate the first feed page once on return and release GS',
    (tester) async {
      tester.view.physicalSize = const Size(390, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final fixture = GenerationFixture()..state = 'succeeded';
      addTearDown(fixture.client.close);
      final requests = <http.Request>[];
      final feedClient = MockClient((request) async {
        requests.add(request);
        Map<String, Object?> row(String id, String title) => {
          'id': id,
          'title': title,
          'sport': 'バスケットボール',
          'description': '公開フィード',
          'preview_url': 'https://media.example.invalid/$id/preview.webm',
          'scene_url': 'https://media.example.invalid/$id/scene.json',
        };
        return fixture.reply({
          'clips': [
            if (fixture.published) row('generated-$generationJobId', '掲載したGS'),
            row('existing', '既存のハイライト'),
          ],
        });
      });
      addTearDown(feedClient.close);
      final repository = ApiClipRepository(
        endpoint: 'https://app.example.invalid/api/v1/clips',
        client: feedClient,
      );
      final videos = <FakeViewer>[], scenes = <FakeViewer>[];
      await tester.pumpWidget(
        RangleApp(
          repository: repository,
          community: fixture.community,
          generation: fixture.api,
          viewerFactory: (_) {
            final viewer = FakeViewer();
            videos.add(viewer);
            return viewer;
          },
          sceneFactory: (_) {
            final scene = FakeViewer(surfaceKey: 'scene-${scenes.length}');
            scenes.add(scene);
            return scene;
          },
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('既存のハイライト'), findsOneWidget);
      expect(requests, hasLength(1));
      expect(requests.single.headers['cache-control'], isNull);
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      final originalScene = scenes.single;
      await tester.tap(find.byTooltip('GSを生成'));
      await tester.pumpAndSettle();
      expect(originalScene.disposed, isTrue);
      expect(videos.first.disposed, isTrue);
      await tapText(tester, 'フィードに掲載');
      expect(requests, hasLength(1));
      await tester.tap(find.byType(BackButton));
      await tester.pumpAndSettle();
      expect(find.byType(GenerationScreen), findsNothing);
      expect(find.text('掲載したGS'), findsOneWidget);
      expect(requests, hasLength(2));
      expect(requests.last.headers['cache-control'], 'no-cache');
      expect(videos.last.disposed, isFalse);
      await tester.tap(find.text('自由視点で見る'));
      await tester.pumpAndSettle();
      final publishedScene = scenes.last;
      expect(publishedScene, isNot(same(originalScene)));
      expect(publishedScene.disposed, isFalse);
      await tester.tap(find.byTooltip('GSを生成'));
      await tester.pumpAndSettle();
      expect(publishedScene.disposed, isTrue);
      await tapText(tester, '掲載を取り下げる');
      expect(requests, hasLength(2));
      await tester.tap(find.byType(BackButton));
      await tester.pumpAndSettle();
      expect(find.text('既存のハイライト'), findsOneWidget);
      expect(find.text('掲載したGS'), findsNothing);
      expect(requests, hasLength(3));
      expect(requests.last.headers['cache-control'], 'no-cache');
      await tester.tap(find.byTooltip('GSを生成'));
      await tester.pumpAndSettle();
      await tester.tap(find.byType(BackButton));
      await tester.pumpAndSettle();
      expect(requests, hasLength(3));
      await repository.loadPage();
      expect(requests.last.headers['cache-control'], isNull);
      expect(requests.map((request) => request.url.toString()).toSet(), {
        'https://app.example.invalid/api/v1/clips?limit=20',
      });
      expect(fixture.publications, [true, false]);
      expect(fixture.creates, isEmpty);
      expect(tester.takeException(), isNull);
    },
  );
}
