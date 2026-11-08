/// Feed metadata is separate from media rendering and the transport manifest.
class Clip {
  const Clip({
    required this.id,
    required this.title,
    required this.sport,
    required this.description,
    this.previewUrl,
    this.sceneUrl,
    this.tags = const [],
  });

  final String id;
  final String title;
  final String sport;
  final String description;
  final List<String> tags;

  /// Ordinary, browser-playable video. Never a Gaussian transport payload.
  final Uri? previewUrl;
  final Uri? sceneUrl;
}
