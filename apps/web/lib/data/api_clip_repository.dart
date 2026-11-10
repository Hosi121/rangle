import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/clip.dart';
import '../models/sports_context.dart';
import '../viewer/playback_state.dart';
import 'clip_repository.dart';

/// Loads one bounded public feed page. Media URLs come from the API catalog.
class ApiClipRepository
    implements PagedClipRepository, RefreshableClipRepository {
  ApiClipRepository({required this.endpoint, this.client, this.publisherId});

  final String endpoint;
  final http.Client? client;
  final String? publisherId;
  int _cacheEpoch = 0, _validatedCacheEpoch = 0;

  @override
  void invalidateCache() => _cacheEpoch++;

  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async =>
      (await loadPage(query: query, tag: tag, clipId: clipId, limit: 50)).clips;

  @override
  Future<ClipPage> loadPage({
    String query = '',
    String tag = '',
    String? clipId,
    String? cursor,
    int limit = 20,
    Future<void>? abortTrigger,
  }) async {
    if (limit < 1 || limit > 50) {
      throw const FormatException('Invalid feed page size.');
    }
    if (publisherId != null &&
        !RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(publisherId!)) {
      throw const FormatException('Invalid publisher ID.');
    }
    if (cursor != null && (cursor.isEmpty || cursor.length > 128)) {
      throw const FormatException('Invalid feed cursor.');
    }
    final parsed = Uri.tryParse(endpoint);
    if (parsed == null) throw const FormatException('Invalid feed URL.');
    final source = Uri.base.resolveUri(parsed);
    _requireHttp(source);
    final cacheEpoch = _cacheEpoch;
    final revalidate = cursor == null && cacheEpoch != _validatedCacheEpoch;
    final httpClient = client ?? http.Client();
    final abort = Completer<void>();
    if (abortTrigger != null) {
      unawaited(
        abortTrigger.then((_) {
          if (!abort.isCompleted) abort.complete();
        }),
      );
    }
    Future<http.Response> get(Uri uri) async {
      final request = http.AbortableRequest(
        'GET',
        uri,
        abortTrigger: abort.future,
      )..headers['Accept'] = 'application/json';
      if (revalidate) request.headers['Cache-Control'] = 'no-cache';
      return http.Response.fromStream(await httpClient.send(request));
    }

    try {
      final response = await get(
        source.replace(
          queryParameters: {
            'limit': '$limit',
            if (query.isNotEmpty) 'q': query,
            if (tag.isNotEmpty) 'tag': tag,
            'publisher': ?publisherId,
            'cursor': ?cursor,
          },
        ),
      ).timeout(const Duration(seconds: 10));
      if (response.statusCode == 400 && cursor != null) {
        throw const StaleClipCursor();
      }
      if (response.statusCode != 200) {
        throw http.ClientException('Clip feed request failed.', source);
      }
      final document = jsonDecode(utf8.decode(response.bodyBytes));
      if (document is! Map<String, dynamic> || document['clips'] is! List) {
        throw const FormatException('Invalid clip feed.');
      }
      final rows = document['clips'] as List;
      if (rows.length > limit) {
        throw const FormatException('Feed page exceeds its limit.');
      }
      final ids = <String>{};
      Clip parse(Object? row) {
        if (row is! Map<String, dynamic>) {
          throw const FormatException('Invalid clip metadata.');
        }
        String text(String key, {bool emptyAllowed = false}) {
          final value = row[key];
          if (value is! String || (!emptyAllowed && value.trim().isEmpty)) {
            throw FormatException('Invalid clip $key.');
          }
          return value;
        }

        Uri? media(String key) {
          final value = row[key];
          if (value == null) return null;
          if (value is! String || value.isEmpty) {
            throw FormatException('Invalid clip $key.');
          }
          final uri = source.resolve(value);
          _requireHttp(uri);
          return uri;
        }

        final id = text('id');
        final revision = row['revision'] as String? ?? '';
        final preview = media('preview_url'), scene = media('scene_url');
        final manifest = media('manifest_url');
        final codec = row['codec'];
        if (codec != null && (codec is! String || codec.trim().isEmpty)) {
          throw const FormatException('Invalid clip codec.');
        }
        // Published RGS1 bundle contract: the preview and model use the same
        // clip-relative clock. Binding includes origin, clip and revision.
        final prefix = '/assets/$id/$revision/full/';
        final paired =
            (row['codec'] == 'rangle-temporal-rgs1' ||
                row['codec'] == 'rangle-temporal-rgs1-segments-v1') &&
            RegExp(r'^[A-Za-z0-9_-]+$').hasMatch(id) &&
            RegExp(r'^[A-Za-z0-9_-]+$').hasMatch(revision) &&
            preview != null &&
            scene != null &&
            preview.origin == scene.origin &&
            !preview.hasQuery &&
            !scene.hasQuery &&
            preview.path == '${prefix}preview.webm' &&
            scene.path == '${prefix}scene.json';
        const previewHashName = 'vru-basketball-f53be0fb17f96e64.webm';
        final knownPreview =
            preview != null &&
            (preview.path == '/previews/$previewHashName' ||
                preview.path == '/assets/assets/samples/basketball.webm');
        final knownLegacy =
            knownPreview &&
            scene != null &&
            preview.origin == scene.origin &&
            !preview.hasQuery &&
            !scene.hasQuery &&
            (scene.path ==
                    '/assets/basketball/baseline6000-reference270/full/scene.json' ||
                (RegExp(r'^generated-[a-f0-9]{32}$').hasMatch(id) &&
                    scene.path == '/generated/${id.substring(10)}/scene.json'));
        final clip = Clip(
          id: id,
          title: text('title'),
          sport: text('sport'),
          description: text('description', emptyAllowed: true),
          previewUrl: preview,
          sceneUrl: scene,
          manifestUrl: manifest,
          codec: codec as String?,
          revision: revision,
          playbackMapping: paired || knownLegacy
              ? const PreviewSceneMapping()
              : null,
          tags: tags(row['tags']),
          context: row['context'] == null
              ? null
              : row['context'] is Map<String, dynamic>
              ? SportsContext.fromJson(row['context'] as Map<String, dynamic>)
              : throw const FormatException('Invalid sporting context.'),
        );
        if (publisherId != null && clip.context?.publisher?.id != publisherId) {
          throw const FormatException('Publisher clip mismatch.');
        }
        return clip;
      }

      final nextCursor = document['next_cursor'];
      if (nextCursor != null &&
          (nextCursor is! String ||
              nextCursor.isEmpty ||
              nextCursor.length > 128)) {
        throw const FormatException('Invalid next cursor.');
      }
      final clips = rows.map(parse).where((clip) => ids.add(clip.id)).toList();
      if (clipId != null && cursor == null) {
        if (!RegExp(r'^[a-zA-Z0-9_-]{1,64}$').hasMatch(clipId)) {
          throw const FormatException('Invalid shared clip ID.');
        }
        final detail = Uri(
          scheme: source.scheme,
          host: source.host,
          port: source.hasPort ? source.port : null,
          pathSegments: [...source.pathSegments, clipId],
        );
        final response = await get(detail).timeout(const Duration(seconds: 10));
        if (response.statusCode != 200) {
          throw http.ClientException('Shared clip not found.', detail);
        }
        ids.remove(clipId);
        final shared = parse(jsonDecode(utf8.decode(response.bodyBytes)));
        if (shared.id != clipId) {
          throw const FormatException('Shared clip mismatch.');
        }
        clips.removeWhere((clip) => clip.id == clipId);
        clips.insert(0, shared);
      }
      if (revalidate && cacheEpoch > _validatedCacheEpoch) {
        _validatedCacheEpoch = cacheEpoch;
      }
      return ClipPage(clips, nextCursor: nextCursor as String?);
    } finally {
      if (!abort.isCompleted) abort.complete();
      if (client == null) httpClient.close();
    }
  }

  static List<String> tags(Object? value) {
    if (value == null) return const [];
    if (value is! List ||
        value.length > 10 ||
        value.any((v) => v is! String || v.isEmpty || v.runes.length > 32)) {
      throw const FormatException('Invalid clip tags.');
    }
    return List<String>.unmodifiable(value.cast<String>());
  }

  static void _requireHttp(Uri uri) {
    if (!{'http', 'https'}.contains(uri.scheme) ||
        !uri.hasAuthority ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.hasFragment) {
      throw const FormatException('Feed and media URLs must use HTTP(S).');
    }
  }
}
