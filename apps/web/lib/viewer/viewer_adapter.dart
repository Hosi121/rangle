import 'dart:async';

import 'package:flutter/foundation.dart' show ValueListenable;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:video_player/video_player.dart';

import 'playback_state.dart';
export 'playback_state.dart';

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
    this.prepared = false,
    this.timeline,
    this.playIntent,
    this.aspectRatio = 1,
    this.rotationCorrection = 0,
  });

  final ViewerStatus status;
  final Duration position;
  final Duration duration;
  final bool playing;
  final bool canPlay;
  final bool muted;
  final bool buffering;
  final Uri? source;
  final bool prepared;
  final ViewerTimeline? timeline;
  final bool? playIntent;
  final double aspectRatio;
  final int rotationCorrection;

  /// State used by the surface and buttons. The playhead has its own listener.
  Object get presentation => (
    status,
    duration,
    playing,
    canPlay,
    muted,
    buffering,
    source,
    prepared,
    timeline,
    playIntent,
    aspectRatio,
    rotationCorrection,
  );

  @override
  bool operator ==(Object other) =>
      other is ViewerValue &&
      status == other.status &&
      position == other.position &&
      duration == other.duration &&
      playing == other.playing &&
      canPlay == other.canPlay &&
      muted == other.muted &&
      buffering == other.buffering &&
      source == other.source &&
      prepared == other.prepared &&
      timeline == other.timeline &&
      playIntent == other.playIntent &&
      aspectRatio == other.aspectRatio &&
      rotationCorrection == other.rotationCorrection;

  @override
  int get hashCode => Object.hash(
    status,
    position,
    duration,
    playing,
    canPlay,
    muted,
    buffering,
    source,
    prepared,
    timeline,
    playIntent,
    aspectRatio,
    rotationCorrection,
  );
}

/// Shared lifecycle and time controls for ordinary video and free-view scenes.
abstract class ViewerAdapter extends ChangeNotifier
    implements ValueListenable<ViewerValue> {
  @override
  ViewerValue get value;
  Future<void> initialize();
  Future<void> togglePlayback();
  Future<void> seek(Duration position);
  Future<void> toggleMute();
  Future<void> pause();
  Future<void> resume() async {}
  Future<bool> updateSource(Uri source, {String? sha256}) async => false;
  void configureTransportIdentity(Map<String, String>? identity) {}
  void configureContinuity(Uri preview, PreviewSceneMapping? mapping) {}
  Future<PlaybackState> capturePlayback() async =>
      PlaybackState(value.position, value.playIntent ?? value.playing);
  Future<bool> restorePlayback(PlaybackState state) async {
    await seek(state.position);
    if (value.canPlay && value.playing != state.playing) await togglePlayback();
    return true;
  }

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
  PlaybackState? _restoreTarget;
  Completer<bool>? _restoration;
  Future<void>? _restoreRunner;
  bool _initializing = true;
  bool _playIntent = false;

  @override
  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {
    try {
      await _controller.initialize().timeout(const Duration(seconds: 15));
      if (_disposed || _failed) return;
      await _configurePlayback(autoplay: _restoreTarget == null);
      if (_restoreTarget != null) await _applyRestore();
      if (!_visible && _controller.value.isInitialized) {
        await _controller.pause();
      }
      _initializing = false;
      if (_restoration != null) unawaited(_applyRestore());
      _update();
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
    _playIntent = true;
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
    _playIntent = false;
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
    final next = ViewerValue(
      status: media.isInitialized && !_initializing && _restoration == null
          ? ViewerStatus.ready
          : ViewerStatus.loading,
      position: media.position,
      duration: media.duration,
      playing: media.isPlaying,
      muted: media.volume == 0,
      buffering: media.isBuffering,
      playIntent: _playIntent,
      aspectRatio: media.aspectRatio,
      rotationCorrection: media.rotationCorrection,
    );
    if (next == _value) return;
    _value = next;
    notifyListeners();
  }

  void _fail() {
    if (_disposed || _failed) return;
    _failed = true;
    _restoration?.complete(false);
    _restoration = null;
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
    _playIntent = !value.playing;
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
  Future<PlaybackState> capturePlayback() async {
    try {
      final position = await _controller.position;
      return PlaybackState(position ?? value.position, _playIntent);
    } on Object {
      return super.capturePlayback();
    }
  }

  @override
  Future<bool> restorePlayback(PlaybackState state) {
    if (_disposed || _failed) return Future.value(false);
    _restoration?.complete(false);
    final completion = _restoration = Completer<bool>();
    _restoreTarget = state;
    _playIntent = state.playing;
    if (!_initializing) unawaited(_applyRestore());
    return completion.future;
  }

  Future<void> _applyRestore() =>
      _restoreRunner ??= _drainRestores().whenComplete(() {
        _restoreRunner = null;
        if (_restoration != null && !_disposed && !_failed) {
          unawaited(_applyRestore());
        }
      });

  Future<void> _drainRestores() async {
    while (!_disposed && !_failed && _restoration != null) {
      final target = _restoreTarget!, completion = _restoration!;
      _playIntent = target.playing;
      try {
        final end = _controller.value.duration;
        final position = target.position < Duration.zero
            ? Duration.zero
            : target.position > end
            ? end
            : target.position;
        await _controller.seekTo(position);
        if (_disposed) return;
        if (!identical(completion, _restoration)) continue;
        if (target.playing && _visible) {
          final controller = _controller, recovery = _autoplayRecovery;
          _autoplayPending = true;
          try {
            await controller.play();
            if (!identical(controller, _controller) ||
                _autoplayRecovery != recovery) {
              await _autoplayRecovery;
              if (identical(completion, _restoration)) {
                _restoreTarget = PlaybackState(target.position, false);
                _playIntent = false;
              }
              continue;
            }
          } on Object catch (error) {
            if (!_isAutoplayPolicyError(error)) rethrow;
            await _recoverAutoplay();
            if (identical(completion, _restoration)) {
              _restoreTarget = PlaybackState(target.position, false);
              _playIntent = false;
            }
            continue;
          }
        } else {
          await _controller.pause();
        }
        if (_disposed) return;
        if (!identical(completion, _restoration)) continue;
        if (!_visible) await _controller.pause();
        if (_disposed) return;
        if (!identical(completion, _restoration)) continue;
        _restoration = null;
        _restoreTarget = null;
        _update();
        completion.complete(true);
      } on Object {
        _fail();
      }
    }
  }

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
    _restoration?.complete(false);
    _restoration = null;
    _controller.removeListener(_update);
    unawaited(_release());
    super.dispose();
  }
}
