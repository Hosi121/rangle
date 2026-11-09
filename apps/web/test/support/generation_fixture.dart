import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/community/community_api.dart';
import 'package:rangle_web/generation/generation_api.dart';

const generationJobId = '0123456789abcdef0123456789abcdef';
Map<String, Object?> generationJob(String state, {String mode = 'static'}) => {
  'id': generationJobId,
  'preset': 'reviewed-sports',
  'state': state,
  'mode': mode,
  'created_at': '2026-11-09T00:00:00Z',
  'updated_at': '2026-11-09T00:00:01Z',
};

Map<String, Object?> generationResult(
  String state, {
  String mode = 'static',
}) => {
  'job': generationJob(state, mode: mode),
  if (state == 'succeeded') ...{
    'viewer_url': '/demo/?clip=generated-$generationJobId',
    'scene_url':
        'https://media.example.invalid/generated/$generationJobId/scene.json',
  },
};

class GenerationFixture {
  Map<String, String>? user = {'id': 'viewer', 'name': 'テストユーザー'};
  String? state;
  String mode = 'static';
  String? createError, pollError;
  int? presetsStatus;
  bool dropFirstCreate = false;
  Completer<http.Response>? pendingCreate;
  final creates = <Map<String, dynamic>>[];
  final requests = <http.Request>[];
  int polls = 0, deletes = 0;

  http.Response reply(Object data, [int status = 200]) => http.Response.bytes(
    utf8.encode(jsonEncode(data)),
    status,
    headers: {'Content-Type': 'application/json'},
  );

  late final client = MockClient((request) async {
    requests.add(request);
    final path = request.url.path;
    if (path.endsWith('/auth/me')) {
      return reply({'user': user, 'available': true});
    }
    if (path.endsWith('/auth/login')) {
      user = {'id': 'viewer', 'name': 'テストユーザー'};
      return reply({'user': user});
    }
    if (path.endsWith('/auth/logout')) {
      user = null;
      return http.Response('', 204);
    }
    if (path.endsWith('/engagement')) {
      return reply({'likes': 0, 'comments': 0, 'liked': false});
    }
    if (path.endsWith('/generation/presets')) {
      if (presetsStatus != null) {
        return http.Response('<html>Not found</html>', presetsStatus!);
      }
      return reply({
        'available': true,
        'presets': [
          {
            'id': 'reviewed-sports',
            'mode': mode,
            'title': 'スポーツのサンプル',
            'sport': 'バスケットボール',
            'description': '確認済みの複数視点データです。',
            'source': 'https://dataset.example.invalid/source',
            'license': 'CC BY 4.0',
          },
        ],
        'limits': {
          'concurrent_jobs': 1,
          'runtime_seconds': 1800,
          'attempts': 10,
        },
      });
    }
    if (path.endsWith('/generation/jobs') && request.method == 'GET') {
      return reply({
        'jobs': [if (state != null) generationJob(state!, mode: mode)],
      });
    }
    if (path.endsWith('/generation/jobs') && request.method == 'POST') {
      creates.add(jsonDecode(request.body) as Map<String, dynamic>);
      if (pendingCreate != null) return pendingCreate!.future;
      if (createError != null) {
        return reply({
          'error': createError,
        }, createError == 'rate_limited' ? 429 : 409);
      }
      state = 'queued';
      if (dropFirstCreate && creates.length == 1) {
        throw http.ClientException('Response lost after admission');
      }
      return reply(generationResult(state!, mode: mode), 202);
    }
    if (path.endsWith('/generation/jobs/$generationJobId')) {
      if (request.method == 'DELETE') {
        deletes++;
        state = 'cancelling';
        return reply(generationResult(state!, mode: mode), 202);
      }
      polls++;
      if (pollError != null) {
        return reply({
          'error': pollError,
        }, pollError == 'not_authenticated' ? 401 : 429);
      }
      return reply(generationResult(state ?? 'running', mode: mode));
    }
    throw StateError('Unexpected request: ${request.method} ${request.url}');
  });

  CommunityApi get community => CommunityApi(
    endpoint: Uri.parse('https://app.example.invalid/api/v1/'),
    client: client,
  );
  GenerationApi get api => GenerationApi(
    endpoint: Uri.parse('https://app.example.invalid/api/v1/'),
    client: client,
  );
}
