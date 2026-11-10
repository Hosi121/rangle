import 'dart:async';

import 'package:flutter/foundation.dart';

import 'thumbnail_source.dart';

/// Throttles hover movement into one decoder job and one latest pending frame.
/// At most 16 small JPEGs are retained, at the requested source timestamps.
class ThumbnailPreview extends ChangeNotifier {
  ThumbnailPreview(this._createSource);

  final ThumbnailSource Function() _createSource;
  final _cache = <int, Uint8List?>{};
  ThumbnailSource? _source;
  Timer? _throttle;
  Timer? _idleRelease;
  int? _requested;
  int? _pending;
  bool _busy = false;
  bool _readyToDecode = false;
  bool _disposed = false;
  int _generation = 0;
  Uint8List? image;

  void preview(Duration position) {
    if (_disposed) return;
    final key = position.inMilliseconds;
    if (_requested == key) return;
    _idleRelease?.cancel();
    _idleRelease = null;
    _requested = key;
    image = null;
    if (_cache.containsKey(key)) {
      _pending = null;
      image = _cache.remove(key);
      _cache[key] = image;
      _scheduleIdleRelease();
    } else {
      _pending = key;
      if (_readyToDecode) {
        unawaited(_drain());
      } else {
        _schedule();
      }
    }
    notifyListeners();
  }

  void _releaseSource() {
    _idleRelease?.cancel();
    _idleRelease = null;
    _generation++;
    _source?.dispose();
    _source = null;
    _busy = false;
  }

  void _scheduleIdleRelease() {
    _idleRelease?.cancel();
    if (_disposed || _source == null || _busy || _pending != null) return;
    // Crossing an iframe can swallow Flutter's pointer-exit notification.
    // Bound decoder retention independently, without hiding the cached image.
    _idleRelease = Timer(const Duration(seconds: 1), _releaseSource);
  }

  void hide() {
    // Keep the small JPEG cache, but stop the dedicated media fetch/decoder.
    // A later uncached hover starts a fresh source without waiting on old work.
    _releaseSource();
    _requested = null;
    _pending = null;
    _throttle?.cancel();
    _throttle = null;
    _readyToDecode = false;
    image = null;
    // The widget hiding the preview already rebuilds; do not notify on dispose.
  }

  void _schedule() {
    // Movement updates the pending timestamp without restarting this interval.
    _throttle ??= Timer(const Duration(milliseconds: 80), () {
      _throttle = null;
      _readyToDecode = true;
      unawaited(_drain());
    });
  }

  Future<void> _drain() async {
    if (_busy || _disposed || _pending == null) return;
    _busy = true;
    final generation = _generation;
    try {
      while (_pending != null && _readyToDecode && !_disposed) {
        final key = _pending!;
        _pending = null;
        _readyToDecode = false;
        _schedule();
        Uint8List? bytes;
        try {
          bytes = await (_source ??= _createSource()).frame(
            Duration(milliseconds: key),
          );
        } on Object {
          // Time-only fallback remains available on decoder failure.
        }
        if (_disposed || generation != _generation) return;
        _cache[key] = bytes;
        if (_cache.length > 16) _cache.remove(_cache.keys.first);
        if (_requested == key) {
          image = bytes;
          notifyListeners();
        }
      }
    } finally {
      if (generation == _generation) {
        _busy = false;
        _scheduleIdleRelease();
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    hide();
    _cache.clear();
    super.dispose();
  }
}
