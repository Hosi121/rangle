import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/data/api_clip_repository.dart';

Map<String, Object?> clip() => {
  'id': 'basketball',
  'title': 'VRU Basketball',
  'sport': 'バスケットボール',
  'description': 'AVS-VRU / CC BY 4.0',
  'preview_url': '/assets/assets/samples/basketball.webm',
  'scene_url': '/assets/basketball/v1/full/scene.json',
};

void main() {
  test(
    'API metadata resolves the preview and free-view scene on the API origin',
    () async {
      final client = MockClient((request) async {
        expect(request.url.toString(), 'https://rangle.test/api/v1/clips');
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'clips': [clip()],
            }),
          ),
          200,
        );
      });
      final repository = ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: client,
      );
      final clips = await repository.load();
      expect(clips.single.title, 'VRU Basketball');
      expect(clips.single.sport, 'バスケットボール');
      expect(
        clips.single.previewUrl.toString(),
        'https://rangle.test/assets/assets/samples/basketball.webm',
      );
      expect(
        clips.single.sceneUrl.toString(),
        'https://rangle.test/assets/basketball/v1/full/scene.json',
      );
      client.close();
    },
  );

  test(
    'empty feeds and media that is not yet published are supported',
    () async {
      for (final rows in <List<Object?>>[
        [],
        [
          {...clip(), 'preview_url': null, 'scene_url': null},
        ],
      ]) {
        final client = MockClient(
          (_) async => http.Response.bytes(
            utf8.encode(jsonEncode({'clips': rows})),
            200,
          ),
        );
        final clips = await ApiClipRepository(
          endpoint: 'https://rangle.test/api/v1/clips',
          client: client,
        ).load();
        if (clips.isNotEmpty) {
          expect(clips.single.previewUrl, isNull);
          expect(clips.single.sceneUrl, isNull);
        }
        client.close();
      }
    },
  );

  test('failed HTTP and malformed metadata fail instead of silently using demo data', () async {
    final failed = MockClient((_) async => http.Response('{}', 503));
    await expectLater(
      ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: failed,
      ).load(),
      throwsA(isA<http.ClientException>()),
    );
    failed.close();
    for (final value in <Map<String, Object?>>[
      {},
      {
        'clips': [null],
      },
      {
        'clips': [
          {...clip(), 'title': null},
        ],
      },
      {
        'clips': [
          {...clip(), 'scene_url': 'javascript:alert(1)'},
        ],
      },
      {
        'clips': [clip(), clip()],
      },
    ]) {
      final client = MockClient(
        (_) async => http.Response.bytes(utf8.encode(jsonEncode(value)), 200),
      );
      await expectLater(
        ApiClipRepository(
          endpoint: 'https://rangle.test/api/v1/clips',
          client: client,
        ).load(),
        throwsFormatException,
      );
      client.close();
    }
  });

  test(
    'invalid endpoint schemes are rejected before making a request',
    () async {
      final client = MockClient(
        (_) async => fail('must not request invalid URL'),
      );
      await expectLater(
        ApiClipRepository(
          endpoint: 'file:///tmp/catalog.json',
          client: client,
        ).load(),
        throwsFormatException,
      );
      client.close();
    },
  );
}
