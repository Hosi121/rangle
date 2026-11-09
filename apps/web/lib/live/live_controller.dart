import 'dart:async';

import 'package:flutter/foundation.dart';

import 'live_api.dart';

/// One HTTP operation at a time. Hidden/disposed screens cancel work and timers.
class LiveController extends ChangeNotifier {
  LiveController(this.api, {String? initialId}) : selectedId = initialId;
  final LiveApi api;
  String? selectedId;
  List<LiveSession> sessions = const [];
  LiveSession? current;
  bool reconnecting = false, loading = true;
  bool missing = false;
  bool _waitingForSession = false;
  bool _visible = true, _disposed = false, _busy = false;
  int _epoch = 0;
  String? _etag;
  Timer? _timer;
  Completer<void>? _abort;

  void start() => unawaited(refresh());
  void select(String? id) {
    if (id == selectedId) return;
    _epoch++;
    if (_abort?.isCompleted == false) _abort!.complete();
    _abort = null;
    _timer?.cancel();
    selectedId = id;
    current = null;
    loading = true;
    reconnecting = false;
    missing = false;
    _etag = null;
    notifyListeners();
    unawaited(refresh());
  }

  Future<void> refresh() async {
    if (_disposed || !_visible || _busy) return;
    _timer?.cancel();
    _busy = true;
    final epoch = _epoch, id = selectedId;
    final abort = _abort = Completer<void>();
    try {
      final reply = await api
          .poll(id: id, etag: _etag, abort: abort.future)
          .timeout(
            const Duration(seconds: 10),
            onTimeout: () {
              if (!abort.isCompleted) abort.complete();
              throw TimeoutException('Live poll timed out.');
            },
          );
      if (_disposed || epoch != _epoch || !_visible) return;
      if (reply.sessions != null) {
        if (id == null) {
          sessions = reply.sessions!;
        } else {
          current = reply.sessions!.single;
        }
      }
      _etag = reply.etag;
      reconnecting = false;
      missing = false;
      loading = false;
    } on LiveMissing {
      if (!_disposed && epoch == _epoch && _visible) {
        missing = true;
        reconnecting = false;
        loading = false;
      }
    } on Object {
      if (!_disposed && epoch == _epoch && _visible) {
        reconnecting = true;
        loading = false;
      }
    } finally {
      _busy = false;
      if (identical(_abort, abort)) _abort = null;
      if (!_disposed && _visible) {
        notifyListeners();
        if (epoch != _epoch) {
          unawaited(refresh());
        } else if ((!missing || _waitingForSession) &&
            current?.terminal != true) {
          _timer = Timer(
            const Duration(seconds: 1),
            () => unawaited(refresh()),
          );
        }
      }
    }
  }

  /// Only an authenticated active job can explain a not-yet-created session.
  void waitForSession(bool value) {
    if (_waitingForSession == value || _disposed) return;
    _waitingForSession = value;
    if (missing) {
      _timer?.cancel();
      if (value) unawaited(refresh());
    }
  }

  void visible(bool value) {
    if (_visible == value || _disposed) return;
    _visible = value;
    _epoch++;
    _timer?.cancel();
    if (!value) {
      if (_abort?.isCompleted == false) _abort!.complete();
      _abort = null;
    } else {
      unawaited(refresh());
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _timer?.cancel();
    if (_abort?.isCompleted == false) _abort!.complete();
    super.dispose();
  }
}
