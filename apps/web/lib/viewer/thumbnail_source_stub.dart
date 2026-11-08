import 'dart:typed_data';

import 'thumbnail_source.dart';

ThumbnailSource create(Uri source) => _UnavailableThumbnails();

class _UnavailableThumbnails implements ThumbnailSource {
  @override
  Future<Uint8List?> frame(Duration position) async => null;
  @override
  void dispose() {}
}
