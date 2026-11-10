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
  test('transport metadata reaches the rendering source without replacing video or unknown codecs', () async {
    for (final codec in [
      'rangle-temporal-rgs1',
      'rangle-temporal-rgs1-segments-v1',
      'unsupported',
    ]) {
      final client = MockClient(
        (_) async => http.Response(
          jsonEncode({
            'clips': [
              {
                ...clip(),
                'revision': 'v1',
                'codec': codec,
                'manifest_url':
                    'https://media.example.test/manifests/basketball/v1.json',
              },
            ],
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        ),
      );
      addTearDown(client.close);
      final value = (await ApiClipRepository(
        endpoint: 'https://example.test/api/v1/clips',
        client: client,
      ).load()).single;
      expect(value.codec, codec);
      expect(
        value.manifestUrl.toString(),
        'https://media.example.test/manifests/basketball/v1.json',
      );
      expect(value.previewUrl, isNotNull);
      expect(
        value.freeViewUrl,
        codec == 'unsupported' ? value.sceneUrl : value.manifestUrl,
      );
    }
  });

  test('segmented codec without a manifest keeps video and cannot silently play just its first scene', () async {
    for (final codec in [
      'rangle-temporal-rgs1-segments-v1',
      'rangle-temporal-rgs1',
      'unsupported',
    ]) {
      final client = MockClient(
        (_) async => http.Response(
          jsonEncode({
            'clips': [
              {...clip(), 'codec': codec},
            ],
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        ),
      );
      addTearDown(client.close);
      final value = (await ApiClipRepository(
        endpoint: 'https://example.test/api/v1/clips',
        client: client,
      ).load()).single;
      expect(value.previewUrl, isNotNull);
      expect(
        value.freeViewUrl,
        codec == 'rangle-temporal-rgs1-segments-v1' ? isNull : value.sceneUrl,
      );
    }
  });

  test('only publisher-bound codec, clip, revision and exact paired URLs declare a shared clock', () async {
    final row = {
      ...clip(),
      'id': 'game',
      'revision': 'r1',
      'codec': 'rangle-temporal-rgs1',
      'preview_url': '/assets/game/r1/full/preview.webm',
      'scene_url': '/assets/game/r1/full/scene.json',
    };
    Future<bool> mapped(Map<String, Object?> data) async {
      final client = MockClient(
        (_) async => http.Response(
          jsonEncode({
            'clips': [data],
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        ),
      );
      addTearDown(client.close);
      return (await ApiClipRepository(
            endpoint: 'https://example.test/api/v1/clips',
            client: client,
          ).load()).single.playbackMapping !=
          null;
    }

    expect(await mapped(row), isTrue);
    for (final mutation in [
      {'codec': 'unknown'},
      {'revision': 'r2'},
      {'id': 'other'},
      {'preview_url': 'https://other.test/assets/game/r1/full/preview.webm'},
      {'preview_url': '/assets/game/r1/full/preview.webm?unrelated=1'},
      {'scene_url': '/other/scene.json'},
    ]) {
      expect(await mapped({...row, ...mutation}), isFalse);
    }
  });
  test(
    'publication invalidation revalidates one public first page and retries',
    () async {
      final requests = <http.Request>[];
      var fail = false;
      final client = MockClient((request) async {
        requests.add(request);
        return http.Response(
          jsonEncode({
            'clips': [clip()],
          }),
          fail ? 503 : 200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      });
      addTearDown(client.close);
      final repo = ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: client,
      );
      await repo.loadPage();
      expect(requests.last.headers['cache-control'], isNull);
      repo.invalidateCache();
      fail = true;
      await expectLater(repo.loadPage(), throwsA(isA<http.ClientException>()));
      expect(requests.last.headers['cache-control'], 'no-cache');
      fail = false;
      await repo.loadPage();
      expect(requests.last.headers['cache-control'], 'no-cache');
      await repo.loadPage();
      expect(requests.last.headers['cache-control'], isNull);
      expect(requests.map((r) => r.url.toString()).toSet(), {
        'https://rangle.test/api/v1/clips?limit=20',
      });
    },
  );

  test(
    'an older completion cannot consume a newer cache invalidation',
    () async {
      final pending = Completer<http.Response>();
      final started = Completer<void>();
      final requests = <http.Request>[];
      final client = MockClient((request) async {
        requests.add(request);
        if (requests.length == 1) {
          started.complete();
          return pending.future;
        }
        return http.Response(
          jsonEncode({
            'clips': [clip()],
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      });
      addTearDown(client.close);
      final repo = ApiClipRepository(
        endpoint: 'https://rangle.test/api/v1/clips',
        client: client,
      )..invalidateCache();
      final older = repo.loadPage();
      await started.future;
      repo.invalidateCache();
      pending.complete(
        http.Response(
          jsonEncode({
            'clips': [clip()],
          }),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        ),
      );
      await older;
      await repo.loadPage();
      expect(
        requests.every((r) => r.headers['cache-control'] == 'no-cache'),
        isTrue,
      );
      await repo.loadPage();
      expect(requests.last.headers['cache-control'], isNull);
    },
  );

  test('a late older load cannot roll back a validated cache epoch', () async {
    final pending = Completer<http.Response>();
    final started = Completer<void>();
    final requests = <http.Request>[];
    http.Response response() => http.Response(
      jsonEncode({
        'clips': [clip()],
      }),
      200,
      headers: {'content-type': 'application/json; charset=utf-8'},
    );
    final client = MockClient((request) async {
      requests.add(request);
      if (requests.length == 1) {
        started.complete();
        return pending.future;
      }
      return response();
    });
    addTearDown(client.close);
    final repo = ApiClipRepository(
      endpoint: 'https://rangle.test/api/v1/clips',
      client: client,
    )..invalidateCache();
    final older = repo.loadPage();
    await started.future;
    repo.invalidateCache();
    await repo.loadPage();
    pending.complete(response());
    await older;
    await repo.loadPage();
    expect(requests.map((request) => request.headers['cache-control']), [
      'no-cache',
      'no-cache',
      null,
    ]);
    expect(requests.map((request) => request.url.toString()).toSet(), {
      'https://rangle.test/api/v1/clips?limit=20',
    });
  });

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
