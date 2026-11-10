import 'package:http/http.dart' as http;

import '../data/api_clip_repository.dart';
import '../data/clip_repository.dart';
import '../models/clip.dart';
import '../live/live_api.dart';

/// Compile-time Web configuration. Only an explicit demo build uses samples.
class AppConfig {
  const AppConfig({
    this.demo = false,
    this.apiUrl = '',
    this.previewUrl = '',
    this.sceneUrl = '',
    this.liveMediaOrigin = '',
  });

  const AppConfig.fromEnvironment()
    : demo = const bool.fromEnvironment('RANGLE_DEMO'),
      apiUrl = const String.fromEnvironment('RANGLE_API_URL'),
      previewUrl = const String.fromEnvironment('RANGLE_PREVIEW_URL'),
      sceneUrl = const String.fromEnvironment('RANGLE_SCENE_URL'),
      liveMediaOrigin = const String.fromEnvironment(
        'RANGLE_LIVE_MEDIA_ORIGIN',
      );

  final bool demo;
  final String apiUrl;
  final String previewUrl;
  final String sceneUrl;
  final String liveMediaOrigin;

  ClipRepository createClipRepository({Uri? baseUri, http.Client? client}) {
    if (demo) {
      return DemoClipRepository(previewUrl: previewUrl, sceneUrl: sceneUrl);
    }
    try {
      return ApiClipRepository(
        endpoint: _feedEndpoint(baseUri ?? Uri.base).toString(),
        client: client,
      );
    } on FormatException catch (error) {
      // Report configuration errors through the feed's existing error state.
      return _InvalidFeedRepository(error);
    }
  }

  LiveApi? createLiveApi({Uri? baseUri, http.Client? client}) {
    try {
      final base = baseUri ?? Uri.base;
      final api = _feedEndpoint(base);
      return LiveApi(
        endpoint: api.resolve('/api/v1/live'),
        mediaOrigin: liveMediaOrigin,
        appUri: base,
        client: client,
      );
    } on FormatException {
      return null;
    }
  }

  Uri? communityEndpoint({Uri? baseUri}) {
    try {
      return _feedEndpoint(baseUri ?? Uri.base).resolve('./');
    } on FormatException {
      return null;
    }
  }

  Uri _feedEndpoint(Uri baseUri) {
    final value = apiUrl.trim().isEmpty ? '/api/v1/clips' : apiUrl.trim();
    final parsed = Uri.tryParse(value);
    final rootRelative = value.startsWith('/') && !value.startsWith('//');
    if (parsed == null || (!parsed.hasScheme && !rootRelative)) {
      throw const FormatException(
        'RANGLE_API_URL must be an HTTP(S) URL or a root-relative path.',
      );
    }
    final endpoint = baseUri.resolveUri(parsed);
    if (!{'http', 'https'}.contains(endpoint.scheme) ||
        !endpoint.hasAuthority ||
        endpoint.host.isEmpty ||
        endpoint.userInfo.isNotEmpty ||
        endpoint.hasFragment) {
      throw const FormatException('RANGLE_API_URL must use HTTP(S).');
    }
    return endpoint;
  }
}

class _InvalidFeedRepository implements ClipRepository {
  const _InvalidFeedRepository(this.error);

  final FormatException error;

  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async => throw error;
}
