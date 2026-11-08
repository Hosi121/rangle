import 'dart:convert';

import 'package:http/http.dart' as http;

import '../models/clip.dart';
import 'clip_repository.dart';

/// Loads one bounded public feed page. Media URLs come from the API catalog.
class ApiClipRepository implements ClipRepository {
  ApiClipRepository({required this.endpoint, this.client});

  final String endpoint;
  final http.Client? client;

  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async {
    final parsed = Uri.tryParse(endpoint);
    if (parsed == null) throw const FormatException('Invalid feed URL.');
    final source = Uri.base.resolveUri(parsed);
    _requireHttp(source);
    final httpClient = client ?? http.Client();
    try {
      final response = await httpClient
          .get(
            source.replace(
              queryParameters: {
                ...source.queryParameters,
                'limit': '50',
                if (query.isNotEmpty) 'q': query,
                if (tag.isNotEmpty) 'tag': tag,
              },
            ),
            headers: {'Accept': 'application/json'},
          )
          .timeout(const Duration(seconds: 10));
      if (response.statusCode != 200) {
        throw http.ClientException('Clip feed request failed.', source);
      }
      final document = jsonDecode(utf8.decode(response.bodyBytes));
      if (document is! Map<String, dynamic> || document['clips'] is! List) {
        throw const FormatException('Invalid clip feed.');
      }
      final rows = document['clips'] as List;
      if (rows.length > 50) {
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
        if (!ids.add(id)) throw const FormatException('Duplicate clip ID.');
        return Clip(
          id: id,
          title: text('title'),
          sport: text('sport'),
          description: text('description', emptyAllowed: true),
          previewUrl: media('preview_url'),
          sceneUrl: media('scene_url'),
          tags: tags(row['tags']),
        );
      }

      final clips = rows.map(parse).toList();
      if (clipId != null) {
        if (!RegExp(r'^[a-zA-Z0-9_-]{1,64}$').hasMatch(clipId)) {
          throw const FormatException('Invalid shared clip ID.');
        }
        final detail = source.replace(
          pathSegments: [...source.pathSegments, clipId],
          query: '',
        );
        final response = await httpClient
            .get(detail, headers: {'Accept': 'application/json'})
            .timeout(const Duration(seconds: 10));
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
      return clips;
    } finally {
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
