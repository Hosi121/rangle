import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:video_player/video_player.dart';

enum ViewerStatus { loading, ready, error }

@immutable
class ViewerValue {
  const ViewerValue({
    this.status = ViewerStatus.loading,
    this.position = Duration.zero,
    this.duration = Duration.zero,
    this.playing = false,
    this.canPlay = true,
    this.muted = false,
    this.buffering = false,
    this.source,
  });

  final ViewerStatus status;
  final Duration position;
  final Duration duration;
  final bool playing;
  final bool canPlay;
  final bool muted;
  final bool buffering;
  final Uri? source;
}

/// Shared lifecycle and time controls for ordinary video and free-view scenes.
abstract class ViewerAdapter extends ChangeNotifier {
  ViewerValue get value;
  Future<void> initialize();
  Future<void> togglePlayback();
  Future<void> seek(Duration position);
  Future<void> toggleMute();
  Future<void> pause();
  Future<void> resume() async {}
  Future<bool> updateSource(Uri source, {String? sha256}) async => false;
  Widget buildView();
}

typedef ViewerFactory = ViewerAdapter Function(Uri source);

class VideoPreviewAdapter extends ViewerAdapter {
  VideoPreviewAdapter(Uri source)
    : this.withController(
        _newController(source),
        retryController: () => _newController(source),
      );

  static VideoPlayerController _newController(Uri source) => source.hasScheme
      ? VideoPlayerController.networkUrl(source)
      : VideoPlayerController.asset(source.toString());

  @visibleForTesting
  VideoPreviewAdapter.withController(
    this._controller, {
    VideoPlayerController Function()? retryController,
  }) : _manualControllerFactory = retryController {
    _controller.addListener(_update);
  }

  VideoPlayerController _controller;
  final VideoPlayerController Function()? _manualControllerFactory;
  ViewerValue _value = const ViewerValue();
  bool _disposed = false;
  bool _failed = false;
  bool _visible = true;
  bool _autoplayPending = false;
  Future<void>? _autoplayRecovery;
  Future<void>? _releaseFuture;

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {
    try {
      await _controller.initialize().timeout(const Duration(seconds: 15));
      if (_disposed || _failed) return;
      _update();
      await _configurePlayback(autoplay: true);
    } on Object {
      _fail();
    }
  }

  Future<void> _configurePlayback({required bool autoplay}) async {
    final controller = _controller;
    try {
      await controller.setVolume(0);
      if (_disposed || _failed || controller.value.volume != 0) return;
      await controller.setLooping(true);
    } on Object {
      // An unsupported mute/loop command leaves manual playback available.
      return;
    }
    if (!autoplay || !_visible || _disposed || _failed) return;
    _autoplayPending = true;
    try {
      await controller.play();
    } on Object catch (error) {
      if (!identical(controller, _controller)) return;
      if (_isAutoplayPolicyError(error)) {
        await _recoverAutoplay();
      } else {
        _fail();
      }
    }
  }

  bool _isAutoplayPolicyError(Object error) {
    if (error is PlatformException && error.code == 'NotAllowedError') {
      return true;
    }
    // The web plugin retains the DOMException message, but drops its code.
    final text = error.toString().toLowerCase();
    return text.contains('notallowederror') ||
        text.contains("user didn't interact") ||
        text.contains('play method is not allowed') ||
        text.contains('request is not allowed by the user agent');
  }

  Future<void> _recoverAutoplay() => _autoplayRecovery ??= _restartManual();

  Future<void> _restartManual() async {
    if (_disposed || _failed || _manualControllerFactory == null) {
      _fail();
      return;
    }
    _autoplayPending = false;
    final previous = _controller;
    previous.removeListener(_update);
    _controller = _manualControllerFactory();
    _controller.addListener(_update);
    _value = const ViewerValue(muted: true);
    notifyListeners();
    unawaited(_releaseController(previous));
    try {
      await _controller.initialize().timeout(const Duration(seconds: 15));
      if (_disposed || _failed) return;
      _update();
      await _configurePlayback(autoplay: false);
    } on Object {
      _fail();
    }
  }

  void _update() {
    if (_disposed || _failed) return;
    final media = _controller.value;
    if (media.hasError) {
      if (_autoplayPending && _isAutoplayPolicyError(media.errorDescription!)) {
        unawaited(_recoverAutoplay());
      } else {
        _fail();
      }
      return;
    }
    if (media.position > Duration.zero) _autoplayPending = false;
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
    _autoplayPending = false;
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
  Future<void> pause() async {
    _visible = false;
    await _perform(_controller.pause);
  }

  @override
  Future<void> resume() async => _visible = true;

  @override
  Widget buildView() => Center(
    child: AspectRatio(
      aspectRatio: _controller.value.aspectRatio,
      child: VideoPlayer(_controller),
    ),
  );

  Future<void> _release() => _releaseFuture ??= _releaseController(_controller);

  Future<void> _releaseController(VideoPlayerController controller) async {
    try {
      if (controller.value.isInitialized) await controller.pause();
    } on Object {
      // A failed stream still needs its native player released.
    } finally {
      try {
        await controller.dispose();
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
