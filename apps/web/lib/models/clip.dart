import '../viewer/playback_state.dart';
import 'sports_context.dart';

/// Feed metadata is separate from media rendering and the transport manifest.
class Clip {
  const Clip({
    required this.id,
    required this.title,
    required this.sport,
    required this.description,
    this.previewUrl,
    this.sceneUrl,
    this.manifestUrl,
    this.codec,
    this.tags = const [],
    this.revision = '',
    this.playbackMapping,
    this.context,
  });

  final SportsContext? context;
  final String id;
  final String title;
  final String sport;
  final String description;
  final List<String> tags;
  final String revision;
  final PreviewSceneMapping? playbackMapping;

  /// Ordinary, browser-playable video. Never a Gaussian transport payload.
  final Uri? previewUrl;
  final Uri? sceneUrl;
  final Uri? manifestUrl;
  final String? codec;

  /// A segmented clip must never present only its first scene as the full clip.
  Uri? get freeViewUrl {
    if (codec == 'rangle-temporal-rgs1-segments-v1') {
      return manifestUrl;
    }
    if (codec == 'rangle-temporal-rgs1' && manifestUrl != null) {
      return manifestUrl;
    }
    return sceneUrl;
  }
}
