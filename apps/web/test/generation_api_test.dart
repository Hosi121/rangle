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
