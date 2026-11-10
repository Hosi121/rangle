import 'dart:typed_data';

import 'thumbnail_source_stub.dart'
    if (dart.library.js_interop) 'thumbnail_source_web.dart'
    as platform;

/// A decoder dedicated to preview images; it never touches the main player.
abstract interface class ThumbnailSource {
  Future<Uint8List?> frame(Duration position);
  void dispose();
}

ThumbnailSource createThumbnailSource(Uri source) => platform.create(source);
