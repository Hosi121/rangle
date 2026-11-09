import 'dart:async';

import 'package:flutter/widgets.dart';

import 'viewer_adapter.dart';

/// Borrows a viewer; PreviewViewer remains responsible for its lifetime.
class PlaybackController extends ChangeNotifier {
  ViewerAdapter? _adapter;
  ViewerValue get value => _adapter?.value ?? const ViewerValue();
  void attach(ViewerAdapter adapter) {
    _adapter?.removeListener(_changed);
    _adapter = adapter;
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

  void _changed() => notifyListeners();
  Future<void> seek(Duration position) async => _adapter?.seek(position);
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
