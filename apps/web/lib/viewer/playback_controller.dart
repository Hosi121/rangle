import 'dart:async';

import 'package:flutter/foundation.dart' show ValueListenable;

import 'package:flutter/widgets.dart';
import 'package:flutter/scheduler.dart';

import 'viewer_adapter.dart';

/// Borrows a viewer; PreviewViewer remains responsible for its lifetime.
class PlaybackController extends ChangeNotifier
    implements ValueListenable<ViewerValue> {
  ViewerAdapter? _adapter;
  ViewerValue _lastValue = const ViewerValue();
  @override
  ViewerValue get value => _adapter?.value ?? const ViewerValue();
  void attach(ViewerAdapter adapter) {
    _adapter?.removeListener(_changed);
    _adapter = adapter;
    _lastValue = adapter.value;
    adapter.addListener(_changed);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (identical(_adapter, adapter)) notifyListeners();
    });
  }

  void detach(ViewerAdapter adapter) {
    if (!identical(_adapter, adapter)) return;
    adapter.removeListener(_changed);
    _adapter = null;
  }

  void _changed() {
    final next = value;
    if (next == _lastValue) return;
    _lastValue = next;
    if (SchedulerBinding.instance.schedulerPhase ==
        SchedulerPhase.persistentCallbacks) {
      final adapter = _adapter;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (adapter != null && identical(adapter, _adapter)) notifyListeners();
      });
    } else {
      notifyListeners();
    }
  }

  Future<void> seek(Duration position) async => _adapter?.seek(position);
  Future<PlaybackState> capture() async =>
      await _adapter?.capturePlayback() ??
      PlaybackState(value.position, value.playIntent ?? value.playing);
  Future<bool> restore(PlaybackState state) async =>
      await _adapter?.restorePlayback(state) ?? false;
  Future<void> pausePlayback() async {
    if (value.playing) await _adapter?.togglePlayback();
  }

  Future<void> play() async {
    if (!value.playing && value.canPlay) await _adapter?.togglePlayback();
  }

  Future<void> toggle() async => _adapter?.togglePlayback();
  Future<void> mute() async => _adapter?.toggleMute();
  @override
  void dispose() {
    _adapter?.removeListener(_changed);
    _adapter = null;
    super.dispose();
  }
}
