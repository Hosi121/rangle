import 'dart:async';

import 'package:flutter/material.dart';
import 'package:video_player/video_player.dart';

enum ViewerStatus { loading, ready, error }

@immutable
class ViewerValue {
  const ViewerValue({
    this.status = ViewerStatus.loading,
    this.position = Duration.zero,
    this.duration = Duration.zero,
    this.playing = false,
    this.muted = false,
    this.buffering = false,
  });

  final ViewerStatus status;
  final Duration position;
  final Duration duration;
  final bool playing;
  final bool muted;
  final bool buffering;
}

/// A future 4DGS renderer can implement its own adapter and view here.
/// The current implementation plays ordinary video and offers no camera API.
abstract class ViewerAdapter extends ChangeNotifier {
  ViewerValue get value;
  Future<void> initialize();
  Future<void> togglePlayback();
  Future<void> seek(Duration position);
  Future<void> toggleMute();
  Future<void> pause();
  Widget buildView();
}

typedef ViewerFactory = ViewerAdapter Function(Uri source);

class VideoPreviewAdapter extends ViewerAdapter {
  VideoPreviewAdapter(Uri source)
    : this.withController(VideoPlayerController.networkUrl(source));

  @visibleForTesting
  VideoPreviewAdapter.withController(this._controller) {
    _controller.addListener(_update);
  }

  final VideoPlayerController _controller;
  ViewerValue _value = const ViewerValue();
  bool _disposed = false;
  bool _failed = false;
  Future<void>? _releaseFuture;

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {
    try {
      await _controller.initialize().timeout(const Duration(seconds: 15));
      if (!_disposed) _update();
    } on Object {
      _fail();
    }
  }

  void _update() {
    if (_disposed || _failed) return;
    final media = _controller.value;
    if (media.hasError) {
      _fail();
      return;
    }
    _value = ViewerValue(
      status: media.isInitialized ? ViewerStatus.ready : ViewerStatus.loading,
      position: media.position,
      duration: media.duration,
      playing: media.isPlaying,
      muted: media.volume == 0,
      buffering: media.isBuffering,
    );
    notifyListeners();
  }

  void _fail() {
    if (_disposed || _failed) return;
    _failed = true;
    _controller.removeListener(_update);
    _value = const ViewerValue(status: ViewerStatus.error);
    notifyListeners();
    unawaited(_release());
  }

  Future<void> _perform(Future<void> Function() action) async {
    if (_disposed || value.status != ViewerStatus.ready) return;
    try {
      await action();
    } on Object {
      _fail();
    }
  }

  @override
  Future<void> togglePlayback() => _perform(() async {
    if (value.playing) {
      await _controller.pause();
    } else {
      if (value.duration > Duration.zero && value.position >= value.duration) {
        await _controller.seekTo(Duration.zero);
      }
      await _controller.play();
    }
  });

  @override
  Future<void> seek(Duration position) =>
      _perform(() => _controller.seekTo(position));

  @override
  Future<void> toggleMute() =>
      _perform(() => _controller.setVolume(value.muted ? 1 : 0));

  @override
  Future<void> pause() => _perform(_controller.pause);

  @override
  Widget buildView() => Center(
    child: AspectRatio(
      aspectRatio: _controller.value.aspectRatio,
      child: VideoPlayer(_controller),
    ),
  );

  Future<void> _release() => _releaseFuture ??= _releaseController();

  Future<void> _releaseController() async {
    try {
      if (_controller.value.isInitialized) await _controller.pause();
    } on Object {
      // A failed stream still needs its native player released.
    } finally {
      try {
        await _controller.dispose();
      } on Object {
        debugPrint('Unable to release video preview.');
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _controller.removeListener(_update);
    unawaited(_release());
    super.dispose();
  }
}
