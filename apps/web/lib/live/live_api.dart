import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import '../data/strict_json.dart';

bool liveId(String value) => RegExp(r'^[A-Za-z0-9_-]{1,64}$').hasMatch(value);

Uri _http(Uri uri) {
  if (!{'http', 'https'}.contains(uri.scheme) ||
      !uri.hasAuthority ||
      uri.host.isEmpty ||
      uri.userInfo.isNotEmpty ||
      uri.hasFragment) {
    throw const FormatException('Invalid live URL.');
  }
  return uri;
}

class LiveSegment {
  LiveSegment.fromJson(Map<String, dynamic> row, String session, LiveApi api)
    : sequence = _integer(row['sequence'], 1, 999999),
      start = _number(row['start_seconds']),
      end = _number(row['end_seconds']),
      sha256 = _text(row['model_sha256'], 64),
      bytes = _integer(row['model_bytes'], 1, 33554432),
      capturedAt = _date(row['captured_at']),
      availableAt = _date(row['available_at']),
      scene = api.media(row['scene_url']) {
    _fields(row, [
      'sequence',
      'start_seconds',
      'end_seconds',
      'scene_url',
      'model_sha256',
      'model_bytes',
      'captured_at',
      'available_at',
      'training_seconds',
      'mean_packed_psnr',
      'mean_packed_ssim',
    ]);
    if (end <= start || !RegExp(r'^[a-f0-9]{64}$').hasMatch(sha256)) {
      throw const FormatException('Invalid live segment.');
    }
    _number(row['training_seconds']);
    _number(row['mean_packed_psnr']);
    final ssim = row['mean_packed_ssim'];
    if (ssim is! num ||
        !ssim.isFinite ||
        ssim < -1 ||
        ssim > 1 ||
        availableAt.isBefore(capturedAt)) {
      throw const FormatException('Invalid live measurements or timestamps.');
    }
    final revision =
        '${sequence.toString().padLeft(6, '0')}-${sha256.substring(0, 16)}';
    if (scene.path != '/live-assets/$session/$revision/scene.json' &&
        scene.path != '/live/$session/$revision/scene.json') {
      throw const FormatException('Invalid live scene path.');
    }
  }
  final int sequence, bytes;
  final double start, end;
  final String sha256;
  final DateTime capturedAt, availableAt;
  final Uri scene;
}

class LiveSession {
  LiveSession.fromJson(Map<String, dynamic> row, LiveApi api)
    : id = _text(row['id'], 64),
      title = _text(row['title'], 256),
      state = _text(row['state'], 16),
      updatedAt = _date(row['updated_at']),
      startedAt = _date(row['started_at']),
      latestInput = _number(row['latest_input_time_seconds']) {
    _fields(row, [
      'id',
      'title',
      'state',
      'started_at',
      'updated_at',
      'target_latency_seconds',
      'latest_input_time_seconds',
      'dropped_windows',
      'source',
      'segments',
    ]);
    if (_number(row['target_latency_seconds']) <= 0 ||
        updatedAt.isBefore(startedAt)) {
      throw const FormatException('Invalid live session time.');
    }
    _integer(row['dropped_windows'], 0, 9007199254740991);
    if (!liveId(id) ||
        !{'starting', 'live', 'ended', 'failed'}.contains(state)) {
      throw const FormatException('Invalid live session.');
    }
    final raw = row['segments'];
    if (raw is! List || raw.length > 32) {
      throw const FormatException('Invalid live window.');
    }
    segments = List.unmodifiable(
      raw.map((entry) => LiveSegment.fromJson(_map(entry), id, api)),
    );
    for (var i = 0; i < segments.length; i++) {
      final current = segments[i];
      if (current.end > latestInput + 1e-6 ||
          current.availableAt.isBefore(startedAt) ||
          current.availableAt.isAfter(updatedAt)) {
        throw const FormatException('Invalid live segment time.');
      }
      if (i > 0) {
        final previous = segments[i - 1];
        if (current.sequence <= previous.sequence ||
            current.start < previous.start ||
            current.end <= previous.end ||
            current.capturedAt.isBefore(previous.capturedAt) ||
            current.availableAt.isBefore(previous.availableAt)) {
          throw const FormatException('Invalid live sequence.');
        }
      }
    }
    final source = _map(row['source']);
    _fields(source, ['label', 'url', 'license', 'caveat']);
    attribution =
        '${_text(source['label'], 256)} · ${_text(source['license'], 256)}';
    sourceUrl = _http(Uri.parse(_text(source['url'], 4096)));
    caveat = _text(source['caveat'], 4096);
    if (sourceUrl.scheme != 'https' || sourceUrl.hasQuery) {
      throw const FormatException('Invalid source URL.');
    }
  }
  final String id, title, state;
  final DateTime startedAt, updatedAt;
  final double latestInput;
  late final List<LiveSegment> segments;
  late final String attribution, caveat;
  late final Uri sourceUrl;
  LiveSegment? get latest => segments.isEmpty ? null : segments.last;
  bool get terminal => state == 'ended' || state == 'failed';
}

void _fields(Map<String, dynamic> value, List<String> names) {
  if (value.length != names.length ||
      names.any((name) => !value.containsKey(name) || value[name] == null)) {
    throw const FormatException('Missing, unknown or null live field.');
  }
}

Map<String, dynamic> _map(Object? value) {
  if (value is! Map<String, dynamic>) {
    throw const FormatException('Invalid live document.');
  }
  return value;
}

String _text(Object? value, int max) {
  if (value is! String ||
      value.trim().isEmpty ||
      utf8.encode(value).length > max) {
    throw const FormatException('Invalid live text.');
  }
  return value;
}

double _number(Object? value) {
  if (value is! num || !value.isFinite || value < 0) {
    throw const FormatException('Invalid live time.');
  }
  return value.toDouble();
}

int _integer(Object? value, int min, int max) {
  if (value is! int || value < min || value > max) {
    throw const FormatException('Invalid live count.');
  }
  return value;
}

DateTime _date(Object? value) {
  final text = _text(value, 64);
  final match = RegExp(
    r'^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|\+00:00)$',
  ).firstMatch(text);
  final date = DateTime.tryParse(text);
  if (match == null ||
      date == null ||
      !date.isUtc ||
      [
        date.year,
        date.month,
        date.day,
        date.hour,
        date.minute,
        date.second,
      ].asMap().entries.any(
        (entry) => entry.value != int.parse(match.group(entry.key + 1)!),
      )) {
    throw const FormatException('Invalid live date.');
  }
  return date;
}

class LiveReply {
  const LiveReply(this.sessions, this.etag);

  /// null means the server confirmed the caller's existing snapshot with 304.
  final List<LiveSession>? sessions;
  final String? etag;
}

class LiveMissing implements Exception {
  const LiveMissing();
}

/// Public polling with a fixed byte limit, no redirect trust and cancellable I/O.
class LiveApi {
  LiveApi({
    required this.endpoint,
    String mediaOrigin = '',
    Uri? appUri,
    http.Client? client,
  }) : _httpClient = client {
    _http(endpoint);
    if (endpoint.hasQuery) {
      throw const FormatException('Invalid live endpoint.');
    }
    origins = {endpoint.origin};
    final app = appUri ?? Uri.base;
    if ({'http', 'https'}.contains(app.scheme)) origins.add(app.origin);
    if (mediaOrigin.isNotEmpty) {
      final media = _http(Uri.parse(mediaOrigin));
      if (media.scheme != 'https' ||
          media.port != 443 ||
          (media.path.isNotEmpty && media.path != '/') ||
          media.hasQuery) {
        throw const FormatException('Media origin must not have a path.');
      }
      origins.add(media.origin);
      _mediaOrigin = Uri.parse(media.origin);
    }
  }
  final Uri endpoint;
  final http.Client? _httpClient;
  Uri? _mediaOrigin;
  late final Set<String> origins;

  Uri media(Object? value) {
    final uri = _http(endpoint.resolve(_text(value, 4096)));
    if (!origins.contains(uri.origin) ||
        uri.hasQuery ||
        uri.path.contains('%') ||
        uri.pathSegments.any((part) => part == '.' || part == '..')) {
      throw const FormatException('Untrusted live media.');
    }
    return uri;
  }

  Future<LiveReply> poll({
    String? id,
    String? etag,
    required Future<void> abort,
  }) async {
    if (id != null && !liveId(id)) {
      throw const FormatException('Invalid live ID.');
    }
    final target = id == null
        ? endpoint
        : _mediaOrigin?.resolve('/live-index/$id.json') ??
              endpoint.replace(path: '${endpoint.path}/$id');
    final client = _httpClient ?? http.Client();
    final request = http.AbortableRequest('GET', target, abortTrigger: abort)
      ..followRedirects = false
      ..headers['Accept'] = 'application/json';
    // Cross-origin CDN pointers use the browser's normal cache revalidation.
    if (etag != null && target.origin == endpoint.origin) {
      request.headers['If-None-Match'] = etag;
    }
    try {
      final response = await client.send(request);
      if (response.statusCode == 304) {
        if (etag == null) {
          throw const FormatException('Unexpected live cache response.');
        }
        await response.stream.drain<void>();
        return LiveReply(null, etag);
      }
      if (response.statusCode != 200 ||
          (response.contentLength ?? 0) > 524288) {
        unawaited(
          response.stream.listen((_) {}).cancel().catchError((Object _) {}),
        );
        if (response.statusCode == 404) throw const LiveMissing();
        throw http.ClientException('Live request failed.', target);
      }
      final body = BytesBuilder(copy: false);
      await for (final chunk in response.stream) {
        if (body.length + chunk.length > 524288) {
          throw const FormatException('Live response exceeds limit.');
        }
        body.add(chunk);
      }
      final document = _map(strictJsonDecode(utf8.decode(body.takeBytes())));
      List<LiveSession> sessions;
      if (id != null) {
        final session = LiveSession.fromJson(document, this);
        if (session.id != id) {
          throw const FormatException('Live session mismatch.');
        }
        sessions = [session];
      } else {
        _fields(document, ['version', 'sessions']);
        final rows = document['sessions'];
        if (document['version'] != 1 || rows is! List || rows.length > 8) {
          throw const FormatException('Invalid live index.');
        }
        sessions = rows
            .map((row) => LiveSession.fromJson(_map(row), this))
            .toList();
        if (sessions.map((s) => s.id).toSet().length != sessions.length) {
          throw const FormatException('Duplicate live session.');
        }
      }
      return LiveReply(List.unmodifiable(sessions), response.headers['etag']);
    } finally {
      if (_httpClient == null) client.close();
    }
  }
}
