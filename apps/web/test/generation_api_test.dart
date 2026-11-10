import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/generation/generation_api.dart';

import 'support/generation_fixture.dart';

class _AbortClient extends http.BaseClient {
  bool aborted = false;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    final response = Completer<http.StreamedResponse>();
    (request as http.AbortableRequest).abortTrigger!.then((_) {
      aborted = true;
      response.completeError(http.RequestAbortedException(request.url));
    });
    return response.future;
  }
}

void main() {
  test(
    'publication only acknowledges the requested job and boolean state',
    () async {
      for (final mutate in <void Function(Map<String, Object?>)>[
        (job) => job.remove('published'),
        (job) => job['published'] = false,
        (job) => job['published'] = 'true',
        (job) => job['id'] = 'another-job',
        (job) => job['state'] = 'running',
      ]) {
        final result = generationResult('succeeded', published: true);
        mutate(result['job'] as Map<String, Object?>);
        final client = MockClient(
          (_) async => http.Response.bytes(
            utf8.encode(jsonEncode(result)),
            200,
            headers: {'content-type': 'application/json; charset=utf-8'},
          ),
        );
        addTearDown(client.close);
        final api = GenerationApi(
          endpoint: Uri.parse('https://app.example.invalid/api/v1/'),
          client: client,
        );
        addTearDown(api.close);
        await expectLater(
          api.setPublished(generationJobId, published: true),
          throwsA(
            isA<GenerationFailure>().having(
              (error) => error.code,
              'code',
              'publication_unavailable',
            ),
          ),
        );
      }
    },
  );

  test(
    'publication uses an idempotent boolean PUT and survives history',
    () async {
      final fixture = GenerationFixture()..state = 'succeeded';
      final api = fixture.api;
      final published = await api.setPublished(
        generationJobId,
        published: true,
      );
      expect(published.published, isTrue);
      expect(published.viewerUrl, isNotNull);
      expect(fixture.requests.last.method, 'PUT');
      expect(
        fixture.requests.last.url.path,
        '/api/v1/generation/jobs/$generationJobId/publication',
      );
      expect(jsonDecode(fixture.requests.last.body), {'published': true});
      expect(fixture.requests.last.url.query, isEmpty);
      expect((await api.jobs()).single.published, isTrue);
      expect(
        (await api.setPublished(generationJobId, published: false)).published,
        isFalse,
      );
      final legacy = generationJob('succeeded')..remove('published');
      expect(GenerationJob.fromJson(legacy).published, isFalse);
      api.close();
    },
  );

  test(
    'reviewed dynamic mode survives presets, creation, polling and history',
    () async {
      final fixture = GenerationFixture()..mode = 'dynamic';
      final api = fixture.api;
      final presets = await api.presets();
      expect(presets.presets.single.isDynamic, isTrue);
      final created = await api.create('reviewed-sports', 'a' * 32);
      expect(created.mode, 'dynamic');
      expect(jsonDecode(fixture.requests.last.body), {
        'preset': 'reviewed-sports',
        'request_id': 'a' * 32,
      });
      fixture.state = 'running';
      expect((await api.get(generationJobId)).stateLabel, '4DGSを生成しています');
      expect((await api.jobs()).single.mode, 'dynamic');
      api.close();
    },
  );

  test('legacy immutable jobs remain static when mode is absent', () {
    final row = generationJob('running')..remove('mode');
    final job = GenerationJob.fromJson(row);
    expect(job.mode, 'static');
    expect(job.stateLabel, '3DGSを生成しています');
    expect(
      () => GenerationJob.fromJson({...row, 'mode': 'unexpected'}),
      throwsFormatException,
    );
  });

  test('approved LIVE uses the existing admission request and keeps its viewer in every state', () async {
    final fixture = GenerationFixture()..mode = 'live';
    final api = fixture.api;
    addTearDown(api.close);
    expect((await api.presets()).presets.single.isLive, isTrue);
    final created = await api.create('reviewed-sports', 'a' * 32);
    expect(created.isLive, isTrue);
    expect(jsonDecode(fixture.requests.last.body), {
      'preset': 'reviewed-sports',
      'request_id': 'a' * 32,
    });
    for (final state in [
      'submitting',
      'queued',
      'running',
      'cancelling',
      'succeeded',
      'failed',
      'cancelled',
    ]) {
      fixture.state = state;
      final job = await api.get(generationJobId);
      expect(job.mode, 'live');
      expect(job.viewerUrl.toString(), '/demo/?live=$generationJobId');
      expect((await api.jobs()).single.mode, 'live');
    }
    expect(
      (await api.cancel(generationJobId)).viewerUrl.toString(),
      '/demo/?live=$generationJobId',
    );
  });

  test('LIVE viewer URLs cannot switch sessions, modes, origins or duplicate query keys', () async {
    for (final url in [
      '/demo/?live=${'f' * 32}',
      '/demo/?clip=generated-$generationJobId',
      '/demo/?live=$generationJobId&live=$generationJobId',
      '/demo/?live=$generationJobId&other=1',
      'https://evil.example.invalid/demo/?live=$generationJobId',
    ]) {
      final api = GenerationApi(
        endpoint: Uri.parse('https://app.example.invalid/api/v1/'),
        client: MockClient(
          (_) async => http.Response(
            jsonEncode({
              ...generationResult('running', mode: 'live'),
              'viewer_url': url,
            }),
            200,
          ),
        ),
      );
      addTearDown(api.close);
      await expectLater(
        api.get(generationJobId),
        throwsA(isA<GenerationFailure>()),
      );
    }
  });

  test('oversized JSON is rejected before sending', () async {
    final fixture = GenerationFixture();
    final api = fixture.api;
    await expectLater(
      api.create('x' * 5000, 'a' * 32),
      throwsA(isA<GenerationFailure>()),
    );
    expect(fixture.requests, isEmpty);
    api.close();
  });

  test('generation uses bounded JSON and the existing cookie session without new credentials', () async {
    final fixture = GenerationFixture();
    final api = fixture.api;
    final presets = await api.presets();
    expect(presets.presets.single.title, 'スポーツのサンプル');
    expect(presets.presets.single.license, 'CC BY 4.0');
    final job = await api.create('reviewed-sports', 'a' * 32);
    expect(job.state, 'queued');
    final request = fixture.requests.last;
    expect(request.bodyBytes.length, lessThanOrEqualTo(4096));
    expect(jsonDecode(request.body), {
      'preset': 'reviewed-sports',
      'request_id': 'a' * 32,
    });
    expect(request.headers['cache-control'], 'no-store');
    expect(request.headers.containsKey('authorization'), isFalse);
    expect(request.url.query, isEmpty);
    api.close();
  });

  test(
    'success URL is read from the response wrapper and list stays owner scoped',
    () async {
      final fixture = GenerationFixture()..state = 'succeeded';
      final api = fixture.api;
      final jobs = await api.jobs();
      expect(jobs.single.viewerUrl, isNull);
      expect(
        fixture.requests.last.url.toString(),
        'https://app.example.invalid/api/v1/generation/jobs',
      );
      final result = await api.get(generationJobId);
      expect(
        result.viewerUrl.toString(),
        '/demo/?clip=generated-$generationJobId',
      );
      expect(result.active, isFalse);
      api.close();
    },
  );

  test('disabled HTML backend returns a usable unavailable error', () async {
    final fixture = GenerationFixture()..presetsStatus = 404;
    final api = fixture.api;
    await expectLater(
      api.presets(),
      throwsA(
        isA<GenerationFailure>().having(
          (e) => e.code,
          'code',
          'generation_unavailable',
        ),
      ),
    );
    api.close();
  });

  test(
    'rate limits retain retry timing and unsafe result URLs are rejected',
    () async {
      final client = MockClient((request) async {
        if (request.method == 'POST') {
          return http.Response(
            '{"error":"rate_limited"}',
            429,
            headers: {'retry-after': '12'},
          );
        }
        return http.Response(
          jsonEncode({
            ...generationResult('succeeded'),
            'viewer_url': 'https://elsewhere.invalid/demo/',
          }),
          200,
        );
      });
      final api = GenerationApi(
        endpoint: Uri.parse('https://app.example.invalid/api/v1/'),
        client: client,
      );
      await expectLater(
        api.create('reviewed-sports', 'a' * 32),
        throwsA(
          isA<GenerationFailure>().having(
            (e) => e.retryAfter,
            'retry delay',
            const Duration(seconds: 12),
          ),
        ),
      );
      await expectLater(
        api.get(generationJobId),
        throwsA(isA<GenerationFailure>()),
      );
      api.close();
    },
  );

  test('leaving cancels the in-flight HTTP request', () async {
    final client = _AbortClient();
    final api = GenerationApi(
      endpoint: Uri.parse('https://app.example.invalid/api/v1/'),
      client: client,
    );
    final pending = api.jobs();
    final assertion = expectLater(
      pending,
      throwsA(isA<http.RequestAbortedException>()),
    );
    api.cancelPending();
    await assertion;
    expect(client.aborted, isTrue);
    api.close();
  });
}
