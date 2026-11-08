import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';

import 'package:flutter/widgets.dart';
import 'package:web/web.dart' as web;

import 'viewer_adapter.dart';

ViewerAdapter createAdapter(Uri source) => _FreeViewAdapter(source);

class _FreeViewAdapter extends ViewerAdapter {
  _FreeViewAdapter(this.source);

  final Uri source;
  web.HTMLIFrameElement? _frame;
  late final JSFunction _listener = _onMessage.toJS;
  final _initialized = Completer<void>();
  final _released = Completer<void>();
  ViewerValue _value = const ViewerValue(canPlay: false);
  bool _disposed = false;
  bool _failed = false;
  bool _listening = false;
  bool _hasReadyFrame = false;
  bool _visible = true;
  double _frameRate = 25;
  int _frameCount = 0;
  int _requestedFrame = 0;
  Timer? _timeout;

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {
    _timeout = Timer(const Duration(seconds: 30), _fail);
    await _initialized.future;
  }

  void _attach(Object element) {
    if (_disposed || _failed) return;
    final frame = element as web.HTMLIFrameElement;
    _frame = frame;
    web.window.addEventListener('message', _listener);
    _listening = true;
    frame
      ..title = '自由視点の3Dシーン。ドラッグで回転、スクロールで拡大縮小'
      ..src = Uri.base.resolve('viewer/index.html').toString();
    frame.style
      ..width = '100%'
      ..height = '100%'
      ..border = '0';
    _applyVisibility();
  }

  void _applyVisibility() {
    final frame = _frame;
    if (frame == null) return;
    frame.style
      ..visibility = _visible ? 'visible' : 'hidden'
      ..pointerEvents = _visible ? 'auto' : 'none';
  }

  void _send(String type, [Map<String, Object?> extra = const {}]) {
    _frame?.contentWindow?.postMessage(
      jsonEncode({'channel': 'rangle-scene-v1', 'type': type, ...extra}).toJS,
      web.window.location.origin.toJS,
    );
  }

  void _onMessage(web.Event event) {
    final message = event as web.MessageEvent;
    if (message.origin != web.window.location.origin ||
        !identical(message.source, _frame?.contentWindow)) {
      return;
    }
    try {
      final data = jsonDecode((message.data as JSString).toDart);
      if (data is! Map<String, dynamic> ||
          data['channel'] != 'rangle-scene-v1') {
        return;
      }
      if (data['type'] == 'disposed') {
        if (!_released.isCompleted) _released.complete();
        return;
      }
      if (_disposed || _failed) return;
      switch (data['type']) {
        case 'boot':
          _send('load', {'url': source.toString()});
          if (!_visible) _send('pause');
        case 'loading':
          _timeout?.cancel();
          _timeout = Timer(const Duration(seconds: 30), _fail);
          _value = ViewerValue(
            status: _hasReadyFrame ? ViewerStatus.ready : ViewerStatus.loading,
            position: _value.position,
            duration: _value.duration,
            playing: _value.playing,
            canPlay: _value.canPlay,
            buffering: true,
          );
          notifyListeners();
        case 'ready':
          _frameRate = (data['frameRate'] as num).toDouble();
          final count = data['frameCount'] as int;
          final index = data['frame'] as int;
          if (!_frameRate.isFinite ||
              _frameRate <= 0 ||
              count <= 0 ||
              index < 0 ||
              index >= count) {
            return _fail();
          }
          _frameCount = count;
          if (index != _requestedFrame) return;
          _timeout?.cancel();
          _hasReadyFrame = true;
          _value = ViewerValue(
            status: ViewerStatus.ready,
            position: Duration(
              microseconds: (index * 1000000 / _frameRate).round(),
            ),
            duration: Duration(
              microseconds: ((count - 1) * 1000000 / _frameRate).round(),
            ),
            canPlay: data['canPlay'] == true,
            playing: data['playing'] is bool
                ? data['playing'] as bool
                : _value.playing,
          );
          if (!_initialized.isCompleted) _initialized.complete();
          notifyListeners();
        case 'playback':
          final seconds = data['positionSeconds'];
          final playing = data['playing'];
          if (!_hasReadyFrame ||
              seconds is! num ||
              !seconds.isFinite ||
              seconds < 0 ||
              playing is! bool) {
            return;
          }
          final position = Duration(
            microseconds: (seconds * 1000000).round().clamp(
              0,
              _value.duration.inMicroseconds,
            ),
          );
          _requestedFrame = (position.inMicroseconds * _frameRate / 1000000)
              .round()
              .clamp(0, _frameCount - 1);
          _value = ViewerValue(
            status: _value.status,
            position: position,
            duration: _value.duration,
            playing: playing,
            canPlay: _value.canPlay,
            buffering: _value.buffering,
          );
          notifyListeners();
        case 'error':
          _fail();
      }
    } on Object {
      // Ignore messages outside this small, same-origin protocol.
    }
  }

  void _fail() {
    if (_disposed || _failed) return;
    _failed = true;
    _timeout?.cancel();
    _value = const ViewerValue(status: ViewerStatus.error, canPlay: false);
    if (!_initialized.isCompleted) _initialized.complete();
    notifyListeners();
    unawaited(_release());
  }

  @override
  Future<void> seek(Duration position) async {
    if (!_disposed && _value.status == ViewerStatus.ready) {
      final frame = (position.inMicroseconds * _frameRate / 1000000)
          .round()
          .clamp(0, _frameCount - 1);
      if (frame == _requestedFrame) return;
      _requestedFrame = frame;
      _value = ViewerValue(
        status: ViewerStatus.ready,
        position: Duration(
          microseconds: (frame * 1000000 / _frameRate).round(),
        ),
        duration: _value.duration,
        playing: _value.playing,
        canPlay: _value.canPlay,
        buffering: true,
      );
      notifyListeners();
      _timeout?.cancel();
      _timeout = Timer(const Duration(seconds: 30), _fail);
      _send('seek', {'frame': frame});
    }
  }

  @override
  Future<void> pause() async {
    _visible = false;
    _applyVisibility();
    _send('pause');
  }

  @override
  Future<void> resume() async {
    if (_disposed || _failed) return;
    _visible = true;
    _applyVisibility();
    _send('resume');
  }

  @override
  Future<void> toggleMute() async {}
  @override
  Future<void> togglePlayback() async {
    if (!_disposed &&
        !_failed &&
        _value.status == ViewerStatus.ready &&
        _value.canPlay) {
      _send('togglePlayback');
    }
  }

  late final Widget _view = HtmlElementView.fromTagName(
    key: ObjectKey(this),
    tagName: 'iframe',
    onElementCreated: _attach,
  );
  @override
  Widget buildView() => _view;

  Future<void>? _releaseFuture;
  Future<void> _release() => _releaseFuture ??= _releaseFrame();
  Future<void> _releaseFrame() async {
    _send('dispose');
    try {
      if (_frame != null) {
        await _released.future.timeout(const Duration(seconds: 2));
      }
    } on TimeoutException {
      // Removing the browsing context also stops workers and rendering.
    } finally {
      if (_listening) web.window.removeEventListener('message', _listener);
      _listening = false;
      _frame?.src = 'about:blank';
      _frame?.remove();
      _frame = null;
    }
  }

  @override
  void dispose() {
    _disposed = true;
    _timeout?.cancel();
    if (!_initialized.isCompleted) _initialized.complete();
    unawaited(_release());
    super.dispose();
  }
}
