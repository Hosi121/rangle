import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/data/api_clip_repository.dart';
import 'package:rangle_web/data/clip_repository.dart';

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
    'opaque next cursors and shared metadata stay on canonical public URLs',
    () async {
      final urls = <Uri>[];
      final client = MockClient((request) async {
        urls.add(request.url);
        return http.Response(
          jsonEncode(
            request.url.path.endsWith('/shared')
                ? {...clip(), 'id': 'shared'}
                : {
                    'clips': [clip(), clip()],
                    'next_cursor': 'opaque_-cursor',
                  },
          ),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      });
      addTearDown(client.close);
      final repo = ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips?cursor=old&q=old',
        client: client,
      );
      final first = await repo.loadPage(clipId: 'shared');
      expect(first.nextCursor, 'opaque_-cursor');
      expect(first.clips.map((clip) => clip.id), ['shared', 'basketball']);
      final next = await repo.loadPage(
        cursor: first.nextCursor,
        clipId: 'shared',
      );
      expect(next.clips.length, 1);
      expect(urls.map((url) => url.toString()), [
        'https://rangle.test/api/v1/clips?limit=20',
        'https://rangle.test/api/v1/clips/shared',
        'https://rangle.test/api/v1/clips?limit=20&cursor=opaque_-cursor',
      ]);
    },
  );

  test('400 is a stale cursor only on a continuation request', () async {
    final client = MockClient((_) async => http.Response('{}', 400));
    addTearDown(client.close);
    final repo = ApiClipRepository(
      endpoint: 'https://rangle.test/api/v1/clips',
      client: client,
    );
    await expectLater(
      repo.loadPage(cursor: 'old'),
      throwsA(isA<StaleClipCursor>()),
    );
    await expectLater(repo.loadPage(), throwsA(isA<http.ClientException>()));
  });

  test(
    'invalid next cursors and oversized pages fail before publication',
    () async {
      for (final cursor in <Object>['', 4, 'a' * 129]) {
        final client = MockClient(
          (_) async => http.Response(
            jsonEncode({'clips': <Object>[], 'next_cursor': cursor}),
            200,
            headers: {'content-type': 'application/json; charset=utf-8'},
          ),
        );
        await expectLater(
          ApiClipRepository(
            endpoint: 'https://rangle.test/api/v1/clips',
            client: client,
          ).loadPage(),
          throwsFormatException,
        );
        client.close();
      }
      final client = MockClient(
        (_) async => http.Response(
          jsonEncode({'clips': List.generate(21, (_) => clip())}),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        ),
      );
      addTearDown(client.close);
      await expectLater(
        ApiClipRepository(
          endpoint: 'https://rangle.test/api/v1/clips',
          client: client,
        ).loadPage(),
        throwsFormatException,
      );
    },
  );

  test(
    'cancellation reaches the HTTP request without closing an injected client',
    () async {
      final entered = Completer<void>();
      final abort = Completer<void>();
      final client = MockClient.streaming((request, _) async {
        expect(request, isA<http.AbortableRequest>());
        entered.complete();
        await (request as http.AbortableRequest).abortTrigger;
        throw http.RequestAbortedException(request.url);
      });
      addTearDown(client.close);
      final repo = ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: client,
      );
      final request = repo.loadPage(abortTrigger: abort.future);
      final rejected = expectLater(
        request,
        throwsA(isA<http.RequestAbortedException>()),
      );
      await entered.future;
      abort.complete();
      await rejected;
    },
  );

  test(
    'search parameters and tags reach the complete server catalog',
    () async {
      final client = MockClient((request) async {
        expect(request.url.queryParameters, {
          'limit': '50',
          'q': '決勝 SLAM',
          'tag': 'Basketball',
        });
        return http.Response.bytes(
          utf8.encode(
            jsonEncode({
              'clips': [
                {
                  ...clip(),
                  'tags': ['Basketball', '決勝'],
                },
              ],
            }),
          ),
          200,
        );
      });
      final rows = await ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: client,
      ).load(query: '決勝 SLAM', tag: 'Basketball');
      expect(rows.single.tags, ['Basketball', '決勝']);
      client.close();
    },
  );
  test(
    'a shared clip is loaded by ID even when absent from the first page',
    () async {
      final client = MockClient(
        (request) async => http.Response.bytes(
          utf8.encode(
            jsonEncode(
              request.url.path.endsWith('/hidden')
                  ? {...clip(), 'id': 'hidden'}
                  : {
                      'clips': [clip()],
                    },
            ),
          ),
          200,
        ),
      );
      final rows = await ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: client,
      ).load(clipId: 'hidden');
      expect(rows.map((row) => row.id), ['hidden', 'basketball']);
      client.close();
    },
  );
  test(
    'API metadata resolves the preview and free-view scene on the API origin',
    () async {
      final client = MockClient((request) async {
        expect(request.url.path, '/api/v1/clips');
        expect(request.url.queryParameters['limit'], '50');
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
