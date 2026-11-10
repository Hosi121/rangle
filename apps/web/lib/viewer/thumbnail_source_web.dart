import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';
import 'dart:math' as math;
import 'dart:typed_data';
import 'dart:ui_web' as ui_web;

import 'package:web/web.dart' as web;

import 'thumbnail_source.dart';

ThumbnailSource create(Uri source) => _VideoThumbnails(source);

class _VideoThumbnails implements ThumbnailSource {
  _VideoThumbnails(this.source);

  final Uri source;
  web.HTMLVideoElement? _video;
  web.HTMLCanvasElement? _canvas;
  void Function()? _cancelWait;
  bool _disposed = false;
  bool _failed = false;

  Future<bool> _wait(String event, void Function() action) async {
    final video = _video!;
    final result = Completer<bool>();
    void finish(bool success) {
      if (!result.isCompleted) result.complete(success);
    }

    final done = ((web.Event _) => finish(true)).toJS;
    final error = ((web.Event _) => finish(false)).toJS;
    video.addEventListener(event, done);
    video.addEventListener('error', error);
    _cancelWait = () => finish(false);
    final timeout = Timer(const Duration(seconds: 5), () => finish(false));
    try {
      action();
      return await result.future;
    } finally {
      timeout.cancel();
      _cancelWait = null;
      video.removeEventListener(event, done);
      video.removeEventListener('error', error);
    }
  }

  @override
  Future<Uint8List?> frame(Duration position) async {
    if (_disposed || _failed) return null;
    try {
      if (_video == null) {
        final video = _video = web.HTMLVideoElement()
          ..crossOrigin = 'anonymous'
          ..muted = true
          ..playsInline = true
          ..preload = position == Duration.zero ? 'auto' : 'metadata';
        final url = source.hasScheme
            ? source.toString()
            : ui_web.assetManager.getAssetUrl(source.toString());
        // At time zero, metadata readiness can precede actual first pixels.
        // Keep the proven initial-frame path; other timestamps seek from metadata.
        final event = position == Duration.zero
            ? 'loadeddata'
            : 'loadedmetadata';
        if (!await _wait(event, () {
          video.src = url;
          video.load();
        })) {
          return _unavailable();
        }
      }
      if (_disposed) return null;
      final video = _video!;
      final seconds = position.inMicroseconds / 1000000;
      if (!video.duration.isFinite || seconds > video.duration || seconds < 0) {
        return null;
      }
      // Metadata preload avoids buffering the clip just to inspect its timeline.
      // seeked follows decoding; do not snapshot the previously decoded frame.
      final target = seconds.clamp(
        0,
        (video.duration - .001).clamp(0, double.infinity),
      );
      if ((video.currentTime - target).abs() > .0001) {
        if (!await _wait('seeked', () => video.currentTime = target)) {
          return _unavailable();
        }
      }
      if (_disposed || video.readyState < 2 || video.videoWidth == 0) {
        return null;
      }
      final canvas = _canvas ??= web.HTMLCanvasElement();
      final scale = 160 / math.max(video.videoWidth, video.videoHeight);
      canvas
        ..width = (video.videoWidth * scale).round().clamp(1, 160)
        ..height = (video.videoHeight * scale).round().clamp(1, 160);
      (canvas.getContext('2d')! as web.CanvasRenderingContext2D).drawImage(
        video,
        0,
        0,
        canvas.width,
        canvas.height,
      );
      final data = canvas.toDataURL('image/jpeg', .7.toJS);
      video.preload = 'metadata';
      return base64Decode(data.substring(data.indexOf(',') + 1));
    } on Object {
      // Includes CORS-tainted canvas, unsupported codecs and interrupted loads.
      return _unavailable();
    }
  }

  Uint8List? _unavailable() {
    _failed = true;
    _release();
    return null;
  }

  void _release() {
    _video?.pause();
    _video?.removeAttribute('src');
    _video?.load();
    _video?.remove();
    _video = null;
    _canvas = null;
  }

  @override
  void dispose() {
    _disposed = true;
    _cancelWait?.call();
    _release();
  }
}
