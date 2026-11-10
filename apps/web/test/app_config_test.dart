import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/config/app_config.dart';
import 'package:rangle_web/data/api_clip_repository.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';

final appUri = Uri.parse('https://app.rangle.test/demo/?clip=published');

void main() {
  test('environment defaults select the API repository', () {
    const config = AppConfig.fromEnvironment();
    expect(config.demo, isFalse);
    expect(
      config.createClipRepository(baseUri: appUri),
      isA<ApiClipRepository>(),
    );
  });

  test('missing or empty API URL requests the same-origin root feed', () async {
    for (final config in [
      const AppConfig(),
      const AppConfig(apiUrl: ''),
      const AppConfig(apiUrl: '   '),
    ]) {
      final client = MockClient((request) async {
        expect(request.url.origin, appUri.origin);
        expect(request.url.path, '/api/v1/clips');
        expect(request.url.queryParameters['clip'], isNull);
        return http.Response('{"clips":[]}', 200);
      });
      addTearDown(client.close);
      expect(
        await config
            .createClipRepository(baseUri: appUri, client: client)
            .load(),
        isEmpty,
      );
      expect(
        config.communityEndpoint(baseUri: appUri),
        Uri.parse('https://app.rangle.test/api/v1/'),
      );
    }
  });

  test(
    'API feed supplies media URLs even when demo overrides are set',
    () async {
      const config = AppConfig(
        apiUrl: 'https://api.rangle.test/api/v1/clips',
        previewUrl: 'https://demo.rangle.test/preview.webm',
        sceneUrl: 'https://demo.rangle.test/scene.json',
      );
      final client = MockClient((request) async {
        expect(request.url.origin, 'https://api.rangle.test');
        return http.Response(
          jsonEncode({
            'clips': [
              {
                'id': 'published',
                'title': 'Published clip',
                'sport': 'Basketball',
                'description': 'Published catalog',
                'preview_url': 'https://media.rangle.test/clip/v1/preview.webm',
                'scene_url': 'https://media.rangle.test/clip/v1/scene.json',
              },
            ],
          }),
          200,
        );
      });
      addTearDown(client.close);
      final clips = await config
          .createClipRepository(baseUri: appUri, client: client)
          .load();
      expect(clips.single.id, 'published');
      expect(
        clips.single.previewUrl.toString(),
        'https://media.rangle.test/clip/v1/preview.webm',
      );
      expect(
        clips.single.sceneUrl.toString(),
        'https://media.rangle.test/clip/v1/scene.json',
      );
      expect(
        config.communityEndpoint(baseUri: appUri),
        Uri.parse('https://api.rangle.test/api/v1/'),
      );
    },
  );

  test('explicit demo mode preserves the bundled sample', () async {
    final repository = const AppConfig(demo: true).createClipRepository();
    expect(repository, isA<DemoClipRepository>());
    final clips = await repository.load();
    expect(clips.first.id, 'demo-01');
    expect(clips.first.previewUrl.toString(), 'assets/samples/basketball.webm');
  });

  test(
    'explicit demo preserves preview and scene overrides without an API',
    () async {
      const config = AppConfig(
        demo: true,
        apiUrl: 'invalid API configuration',
        previewUrl: 'https://demo.rangle.test/preview.webm',
        sceneUrl: 'https://demo.rangle.test/scene.json',
      );
      final client = MockClient(
        (_) async => fail('Demo must not call the API.'),
      );
      addTearDown(client.close);
      final clips = await config.createClipRepository(client: client).load();
      expect(clips.first.id, 'demo-01');
      expect(clips.first.previewUrl.toString(), config.previewUrl);
      expect(clips.first.sceneUrl.toString(), config.sceneUrl);
    },
  );

  test('invalid API URLs fail without a request or sample fallback', () async {
    final client = MockClient(
      (_) async => fail('Invalid URL must not be used.'),
    );
    addTearDown(client.close);
    for (final apiUrl in [
      'https://[',
      'file:///tmp/catalog.json',
      'javascript:alert(1)',
      'api/v1/clips',
      '//api.rangle.test/api/v1/clips',
      'https://user:secret@api.rangle.test/api/v1/clips',
      'https://api.rangle.test/api/v1/clips#feed',
    ]) {
      final config = AppConfig(apiUrl: apiUrl);
      await expectLater(
        config.createClipRepository(baseUri: appUri, client: client).load(),
        throwsFormatException,
        reason: apiUrl,
      );
      expect(config.communityEndpoint(baseUri: appUri), isNull);
    }
  });

  testWidgets('invalid configuration displays the feed error state', (
    tester,
  ) async {
    final repository = const AppConfig(apiUrl: 'https://[')
        .createClipRepository(baseUri: appUri);
    await tester.pumpWidget(RangleApp(repository: repository));
    await tester.pumpAndSettle();
    expect(find.text('ハイライトを読み込めませんでした'), findsOneWidget);
    expect(find.text('VRU Basketball'), findsNothing);
  });

  testWidgets(
    'API failure stays an error after retry without loading samples',
    (tester) async {
      final responses = <Completer<http.Response>>[];
      final client = MockClient((_) {
        final response = Completer<http.Response>();
        responses.add(response);
        return response.future;
      });
      addTearDown(client.close);
      final repository = const AppConfig().createClipRepository(
        baseUri: appUri,
        client: client,
      );
      await tester.pumpWidget(RangleApp(repository: repository));
      responses.single.complete(http.Response('{}', 503));
      await tester.pumpAndSettle();
      expect(find.text('ハイライトを読み込めませんでした'), findsOneWidget);
      await tester.tap(find.text('再読み込み'));
      await tester.pump();
      responses.last.complete(http.Response('{}', 503));
      await tester.pumpAndSettle();
      expect(responses, hasLength(2));
      expect(find.text('ハイライトを読み込めませんでした'), findsOneWidget);
      expect(find.text('VRU Basketball'), findsNothing);
    },
  );

  testWidgets('an unconfigured or empty catalog displays an empty feed', (
    tester,
  ) async {
    final client = MockClient((_) async => http.Response('{"clips":[]}', 200));
    addTearDown(client.close);
    final repository = const AppConfig().createClipRepository(
      baseUri: appUri,
      client: client,
    );
    await tester.pumpWidget(RangleApp(repository: repository));
    await tester.pumpAndSettle();
    expect(find.text('公開されたハイライトはまだありません'), findsOneWidget);
    expect(find.text('VRU Basketball'), findsNothing);
  });
}
