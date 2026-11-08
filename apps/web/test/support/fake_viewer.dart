import 'package:flutter/material.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';

class FakeViewer extends ViewerAdapter {
  FakeViewer({
    ViewerStatus initialStatus = ViewerStatus.ready,
    bool canPlay = true,
    this.surfaceKey = 'real-video-surface',
  }) : _value = ViewerValue(
         status: initialStatus,
         duration: const Duration(seconds: 20),
         canPlay: canPlay,
       );

  ViewerValue _value;
  final String surfaceKey;
  bool disposed = false;
  bool paused = false;
  int pauseCount = 0;
  int resumeCount = 0;
  Duration? lastSeek;

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {}

  void fail() {
    _value = const ViewerValue(status: ViewerStatus.error);
    notifyListeners();
  }

  void becomeReady() {
    _value = const ViewerValue(
      status: ViewerStatus.ready,
      duration: Duration(seconds: 20),
    );
    notifyListeners();
  }

  @override
  Future<void> togglePlayback() async {
    _value = ViewerValue(
      status: _value.status,
      playing: !_value.playing,
      canPlay: _value.canPlay,
      muted: _value.muted,
      position: _value.position,
      duration: _value.duration,
    );
    notifyListeners();
  }

  @override
  Future<void> seek(Duration position) async {
    lastSeek = position;
    _value = ViewerValue(
      status: _value.status,
      position: position,
      duration: _value.duration,
      playing: _value.playing,
      canPlay: _value.canPlay,
      muted: _value.muted,
    );
    notifyListeners();
  }

  @override
  Future<void> toggleMute() async {
    _value = ViewerValue(
      status: _value.status,
      position: _value.position,
      duration: _value.duration,
      playing: _value.playing,
      canPlay: _value.canPlay,
      muted: !_value.muted,
    );
    notifyListeners();
  }

  @override
  Future<void> pause() async {
    pauseCount++;
    paused = true;
  }

  @override
  Future<void> resume() async {
    resumeCount++;
    paused = false;
  }

  @override
  Widget buildView() => ColoredBox(key: Key(surfaceKey), color: Colors.black);

  @override
  void dispose() {
    disposed = true;
    paused = true;
    pauseCount++;
    super.dispose();
  }
}
