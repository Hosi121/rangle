import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

class GenerationFailure implements Exception {
  const GenerationFailure(this.code, {this.retryAfter});
  final String code;
  final Duration? retryAfter;

  String get message => switch (code) {
    'admin_required' => '運営アカウントでログインしてください。',
    'publisher_access_denied' => 'この操作を行う権限がありません。',
    'not_authenticated' => 'ログインしてから、もう一度お試しください。',
    'generation_busy' => '別の生成が進行中です。少し時間をおいてお試しください。',
    'generation_budget_exhausted' => '現在の生成受付枠を使い切りました。',
    'generation_window_ended' => '今回の生成受付は終了しました。',
    'rate_limited' => '操作が続いています。少し時間をおいてお試しください。',
    'request_conflict' => '依頼を確認できませんでした。状態を再読み込みしてください。',
    'generation_unavailable' => '生成機能は準備中です。時間をおいて再読み込みしてください。',
    'publication_unavailable' => '掲載するデータを確認できませんでした。時間をおいて再試行してください。',
    'not_found' => 'この生成結果は見つかりませんでした。',
    _ => '接続を確認して、もう一度お試しください。',
  };
}

String _mode(Object? value) {
  if (value == null || value == '' || value == 'static') return 'static';
  if (value == 'dynamic' || value == 'live') return value as String;
  throw const FormatException('Unsupported generation mode.');
}

class GenerationPreset {
  GenerationPreset.fromJson(Map<String, dynamic> data)
    : id = data['id'] as String,
      title = data['title'] as String,
      sport = data['sport'] as String,
      description = data['description'] as String,
      source = data['source'] as String,
      license = data['license'] as String,
      mode = _mode(data['mode']);
  final String id, title, sport, description, source, license, mode;
  bool get isDynamic => mode == 'dynamic';
  bool get isLive => mode == 'live';
  String get formatLabel => isLive
      ? 'LIVEの自由視点（順次更新）'
      : isDynamic
      ? '動く4DGS（複数時刻）'
      : '静的な3DGS（1時刻）';
}

class GenerationCatalog {
  GenerationCatalog.fromJson(Map<String, dynamic> data)
    : available = data['available'] == true,
      presets = (data['presets'] as List? ?? [])
          .map((row) => GenerationPreset.fromJson(row as Map<String, dynamic>))
          .toList();
  final bool available;
  final List<GenerationPreset> presets;
}

class GenerationJob {
  GenerationJob.fromJson(Map<String, dynamic> data, {this.viewerUrl})
    : id = data['id'] as String,
      preset = data['preset'] as String,
      state = data['state'] as String,
      mode = _mode(data['mode']),
      published = data['published'] as bool? ?? false,
      createdAt = DateTime.parse(data['created_at'] as String),
      progress = data['progress'] == null
          ? null
          : GenerationProgress.fromJson(
              data['progress'] as Map<String, dynamic>,
            );
  final String id, preset, state, mode;
  final DateTime createdAt;
  final Uri? viewerUrl;
  final bool published;
  final GenerationProgress? progress;
  bool get isLive => mode == 'live';

  bool get active =>
      !const ['succeeded', 'failed', 'cancelled'].contains(state);
  String get stateLabel => switch (state) {
    'submitting' => isLive ? 'LIVEの開始を受け付けています' : '生成を受け付けています',
    'queued' => isLive ? 'LIVE用GPUの準備を待っています' : '順番を待っています',
    'running' =>
      isLive
          ? 'LIVEの生成ジョブが稼働中です'
          : mode == 'dynamic'
          ? '4DGSを生成しています'
          : '3DGSを生成しています',
    'cancelling' => '中止しています',
    'succeeded' => isLive ? 'LIVEの生成が終了しました' : '生成が完了しました',
    'failed' => '生成に失敗しました',
    'cancelled' => '生成を中止しました',
    _ => '状態を確認してください',
  };
}

class GenerationProgress {
  GenerationProgress.fromJson(Map<String, dynamic> data)
    : stage = data['stage'] as String,
      completed = data['completed_steps'] as int? ?? 0,
      total = data['total_steps'] as int? ?? 0,
      observedAt = DateTime.parse(data['observed_at'] as String),
      etaSeconds = data['eta_seconds'] as int?,
      stale = data['stale'] as bool {
    if (!const ['preparing', 'training', 'finalizing'].contains(stage) ||
        completed < 0 ||
        total < completed ||
        total > 1000000 ||
        stage == 'training' && (total == 0 || completed == 0) ||
        stage != 'training' && (total != 0 || completed != 0) ||
        etaSeconds != null &&
            (etaSeconds! <= 0 ||
                etaSeconds! > 1800 ||
                stage != 'training' ||
                stale)) {
      throw const FormatException('Invalid generation progress.');
    }
  }
  final String stage;
  final int completed, total;
  final DateTime observedAt;
  final int? etaSeconds;
  final bool stale;
  double? get fraction => stage == 'training' ? completed / total : null;
  String? get etaLabel {
    final seconds = etaSeconds;
    if (seconds == null || stale) return null;
    return seconds >= 60
        ? '学習の残り目安：約${(seconds / 60).ceil()}分'
        : '学習の残り目安：約${(seconds / 10).ceil() * 10}秒';
  }
}

/// Uses the existing same-origin HttpOnly session cookie, with no new auth state.
class GenerationApi {
  GenerationApi({
    required this.endpoint,
    this.publisherAccountId,
    http.Client? client,
  }) : _client = client ?? http.Client(),
       _ownsClient = client == null {
    if (publisherAccountId != null &&
        !RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(publisherAccountId!)) {
      throw const FormatException('Invalid publisher scope.');
    }
  }
  final Uri endpoint;
  final String? publisherAccountId;
  final http.Client _client;
  final bool _ownsClient;
  final _pending = <Completer<void>>{};

  Future<Map<String, dynamic>> _request(
    String method,
    String path, [
    Map<String, Object?>? body,
  ]) async {
    final abort = Completer<void>();
    final message = http.AbortableRequest(
      method,
      endpoint.resolve(
        '${publisherAccountId == null ? 'generation' : 'publishers/$publisherAccountId/generation'}/$path',
      ),
      abortTrigger: abort.future,
    )..headers.addAll({'Accept': 'application/json', 'Cache-Control': 'no-store'});
    if (body != null) {
      message.headers['Content-Type'] = 'application/json';
      message.body = jsonEncode(body);
      if (message.bodyBytes.length > 4096) {
        throw const GenerationFailure('invalid_request');
      }
    }
    _pending.add(abort);
    try {
      final response =
          await (() async => http.Response.fromStream(
            await _client.send(message),
          ))().timeout(
            const Duration(seconds: 25),
            onTimeout: () {
              if (!abort.isCompleted) abort.complete();
              throw const GenerationFailure('network_error');
            },
          );
      Map<String, dynamic> data = {};
      try {
        data =
            jsonDecode(utf8.decode(response.bodyBytes)) as Map<String, dynamic>;
      } on Object {
        if (response.statusCode >= 200 && response.statusCode < 300) {
          throw const GenerationFailure('generation_unavailable');
        }
      }
      if (response.statusCode < 200 || response.statusCode >= 300) {
        final seconds = int.tryParse(response.headers['retry-after'] ?? '');
        throw GenerationFailure(
          data['error'] as String? ??
              (response.statusCode == 404
                  ? 'generation_unavailable'
                  : 'network_error'),
          retryAfter: seconds == null
              ? null
              : Duration(seconds: seconds.clamp(1, 60)),
        );
      }
      return data;
    } on http.RequestAbortedException {
      rethrow;
    } on GenerationFailure {
      rethrow;
    } on Object {
      throw const GenerationFailure('network_error');
    } finally {
      _pending.remove(abort);
    }
  }

  Future<GenerationCatalog> presets() async =>
      GenerationCatalog.fromJson(await _request('GET', 'presets'));
  Future<List<GenerationJob>> jobs() async =>
      ((await _request('GET', 'jobs'))['jobs'] as List)
          .map((row) => GenerationJob.fromJson(row as Map<String, dynamic>))
          .toList();
  Future<GenerationJob> create(String preset, String requestId) async => _job(
    await _request('POST', 'jobs', {'preset': preset, 'request_id': requestId}),
  );
  Future<GenerationJob> get(String id) async =>
      _job(await _request('GET', 'jobs/${Uri.encodeComponent(id)}'));
  Future<GenerationJob> cancel(String id) async =>
      _job(await _request('DELETE', 'jobs/${Uri.encodeComponent(id)}'));
  Future<GenerationJob> setPublished(
    String id, {
    required bool published,
  }) async {
    final data = await _request(
      'PUT',
      'jobs/${Uri.encodeComponent(id)}/publication',
      {'published': published},
    );
    final job = data['job'];
    if (job is! Map<String, dynamic> ||
        job['id'] != id ||
        job['state'] != 'succeeded' ||
        job['published'] != published) {
      throw const GenerationFailure('publication_unavailable');
    }
    return _job(data);
  }

  GenerationJob _job(Map<String, dynamic> data) {
    final row = data['job'] as Map<String, dynamic>;
    final live = _mode(row['mode']) == 'live';
    Uri? viewer;
    if (data['viewer_url'] is String) {
      final uri = Uri.parse(data['viewer_url'] as String);
      final expected = live ? row['id'] : 'generated-${row['id']}';
      if (uri.hasScheme ||
          uri.hasAuthority ||
          uri.hasFragment ||
          uri.path != '/demo/' ||
          uri.queryParametersAll.length != 1 ||
          uri.queryParametersAll.values.single.length != 1 ||
          !RegExp(r'^[a-f0-9]{32}$').hasMatch(row['id'] as String) ||
          uri.queryParameters[live ? 'live' : 'clip'] != expected) {
        throw const GenerationFailure('generation_unavailable');
      }
      viewer = uri;
    }
    return GenerationJob.fromJson(row, viewerUrl: viewer);
  }

  void cancelPending() {
    for (final request in _pending) {
      if (!request.isCompleted) request.complete();
    }
  }

  void close() {
    cancelPending();
    if (_ownsClient) _client.close();
  }
}
