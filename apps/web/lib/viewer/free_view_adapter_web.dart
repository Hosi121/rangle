import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';

import 'package:flutter/widgets.dart';
import 'package:web/web.dart' as web;

import 'live_source_updates.dart';
import 'viewer_adapter.dart';

ViewerAdapter createAdapter(Uri source) => _FreeViewAdapter(source);
ViewerAdapter createLiveAdapter(
  Uri source, {
  required List<String> origins,
  String? sha256,
}) => _FreeViewAdapter(source, live: true, origins: origins, sha256: sha256);

class _FreeViewAdapter extends ViewerAdapter {
  _FreeViewAdapter(
    this.source, {
    this.live = false,
    this.origins = const [],
    this.sha256,
  });

  final Uri source;
  final bool live;
  final List<String> origins;
  final String? sha256;
  late final _updates = LiveSourceUpdates(
    initialSource: source,
    initialSha256: sha256,
    start: (request) => _send('replace', {
      'requestId': request.id,
      'url': request.source.toString(),
      'sha256': request.sha256,
    }),
    cancel: () => _send('cancelReplace'),
  );
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
  Duration get _loadingTimeout => Duration(seconds: live ? 30 : 60);
  bool _deferFirstFrame = false, _prepared = false;
  Map<String, String>? _transportIdentity;
  @override
  void configureTransportIdentity(Map<String, String>? identity) {
    _transportIdentity = identity;
  }

  Uri? _preview;
  PreviewSceneMapping? _mapping;
  ViewerTimeline? _timeline;
  int _presentationId = 0;
  PlaybackState? _restoreTarget;
  Completer<bool>? _restoration;
  Completer<PlaybackState>? _capture;
  Timer? _captureTimeout;
  int _captureId = 0;

  void _finishCapture([PlaybackState? captured]) {
    _captureTimeout?.cancel();
    _capture?.complete(
      captured ??
          PlaybackState(value.position, value.playIntent ?? value.playing),
    );
    _capture = null;
  }

  @override
  Future<PlaybackState> capturePlayback() {
    if (_disposed || _failed || !_hasReadyFrame) return super.capturePlayback();
    _finishCapture();
    final completion = _capture = Completer<PlaybackState>();
    _captureTimeout = Timer(const Duration(seconds: 2), _finishCapture);
    _send('capturePlayback', {'captureId': ++_captureId});
    return completion.future;
  }

  @override
  void configureContinuity(Uri preview, PreviewSceneMapping? mapping) {
    if (live) return;
    _deferFirstFrame = true;
    _preview = Uri.base.resolveUri(preview);
    _mapping = mapping;
  }

  @override
  Future<bool> restorePlayback(PlaybackState state) {
    if (_disposed || _failed) return Future.value(false);
    _restoration?.complete(false);
    _restoration = Completer<bool>();
    _restoreTarget = state;
    _presentationId++;
    _sendPresentation();
    return _restoration!.future;
  }

  void _sendPresentation() {
    if (!_prepared || _restoreTarget == null || _restoration == null) return;
    _requestedFrame = _timeline!.frameAt(_restoreTarget!.position);
    _timeout?.cancel();
    _timeout = Timer(_loadingTimeout, _fail);
    _send('present', {
      'presentationId': _presentationId,
      'frame': _requestedFrame,
      'playing': _restoreTarget!.playing,
    });
  }

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {
    if (!live || _visible) _timeout = Timer(_loadingTimeout, _fail);
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
        case 'captured':
          if (data['captureId'] != _captureId || _capture == null) return;
          final seconds = (data['positionSeconds'] as num).toDouble();
          if (!seconds.isFinite || seconds < 0) return;
          _finishCapture(
            PlaybackState(
              Duration(microseconds: (seconds * 1000000).round()),
              data['playing'] as bool,
            ),
          );
        case 'boot':
          _send('load', {
            'url': source.toString(),
            if (_transportIdentity != null)
              'transportIdentity': _transportIdentity,
            if (_deferFirstFrame) ...{
              'deferFirstFrame': true,
              'previewUrl': _preview.toString(),
              if (_mapping != null) 'mapping': _mapping!.toJson(),
            },
            if (live) ...{'live': true, 'origins': origins, 'sha256': sha256},
          });
          if (!_visible) _send('pause');
        case 'prepared':
          final count = data['frameCount'] as int;
          final rate = (data['frameRate'] as num).toDouble();
          if (count <= 0 || !rate.isFinite || rate <= 0) return _fail();
          final mapping = data['mapping'] as Map<String, dynamic>?;
          PreviewSceneMapping? verified;
          if (mapping != null) {
            final preview = (mapping['previewStartSeconds'] as num).toDouble();
            final scene = (mapping['sceneStartSeconds'] as num).toDouble();
            if (!preview.isFinite ||
                !scene.isFinite ||
                preview < 0 ||
                scene < 0) {
              return _fail();
            }
            verified = PreviewSceneMapping(
              previewStart: Duration(microseconds: (preview * 1000000).round()),
              sceneStart: Duration(microseconds: (scene * 1000000).round()),
            );
          }
          _frameCount = count;
          _frameRate = rate;
          _timeline = ViewerTimeline(
            frameCount: count,
            frameRate: rate,
            mapping: verified,
          );
          _prepared = true;
          _value = ViewerValue(
            status: _value.status,
            position: _value.position,
            duration: _timeline!.atFrame(count - 1),
            canPlay: count > 1,
            playing: _value.playing,
            prepared: true,
            timeline: _timeline,
          );
          notifyListeners();
          _sendPresentation();
        case 'loading':
          _timeout?.cancel();
          if (!live || _visible) {
            _timeout = Timer(_loadingTimeout, _fail);
          }
          _value = ViewerValue(
            status: _hasReadyFrame ? ViewerStatus.ready : ViewerStatus.loading,
            position: _value.position,
            duration: _value.duration,
            playing: _value.playing,
            canPlay: _value.canPlay,
            source: _value.source,
            playIntent: _value.playIntent,
            prepared: _prepared,
            timeline: _timeline,
            buffering: true,
          );
          notifyListeners();
        case 'ready':
        case 'modelReady':
          final replacing = data['type'] == 'modelReady';
          final presentation = data['presentationId'];
          if (presentation != null && presentation != _presentationId) return;
          if (_deferFirstFrame && !_hasReadyFrame && presentation == null) {
            return;
          }
          final request = data['requestId'];
          if (replacing && !_updates.matches(request, data['url'])) {
            return;
          }
          final replacedSource = replacing ? _updates.active!.source : null;
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
          if (!replacing && index != _requestedFrame) return;
          if (replacing) {
            _requestedFrame = index;
          }
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
            playIntent: data['playing'] as bool? ?? _value.playIntent,
            prepared: _prepared,
            timeline: _timeline,
            source: replacing ? replacedSource : _value.source ?? source,
            playing: data['playing'] is bool
                ? data['playing'] as bool
                : _value.playing,
          );
          if (!_initialized.isCompleted) _initialized.complete();
          notifyListeners();
          if (presentation != null) {
            _restoration?.complete(true);
            _restoration = null;
            _restoreTarget = null;
          }
          if (replacing) {
            _updates.finish(request, data['url'], true);
          } else if (live) {
            _updates.initialized();
          }
        case 'replaceError':
          _updates.finish(data['requestId'], data['url'], false);
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
          final next = ViewerValue(
            status: _value.status,
            position: position,
            duration: _value.duration,
            playing: playing,
            playIntent: data['playIntent'] as bool? ?? playing,
            canPlay: _value.canPlay,
            source: _value.source,
            buffering: _value.buffering,
            prepared: _prepared,
            timeline: _timeline,
          );
          if (next != _value) {
            _value = next;
            notifyListeners();
          }
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
    _finishCapture();
    _restoration?.complete(false);
    _restoration = null;
    _timeout?.cancel();
    _updates.close();
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
        source: _value.source,
        playIntent: _value.playIntent,
        buffering: true,
        prepared: _prepared,
        timeline: _timeline,
      );
      notifyListeners();
      _timeout?.cancel();
      _timeout = Timer(_loadingTimeout, _fail);
      _send('seek', {'frame': frame});
    }
  }

  @override
  Future<void> pause() async {
    _visible = false;
    if (live) _updates.visible(false);
    if (live && !_hasReadyFrame) _timeout?.cancel();
    _applyVisibility();
    _send('pause');
  }

  @override
  Future<void> resume() async {
    if (_disposed || _failed) return;
    _visible = true;
    if (live) _updates.visible(true);
    if (live && !_hasReadyFrame) {
      _timeout = Timer(_loadingTimeout, _fail);
    }
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

  @override
  Future<bool> updateSource(Uri next, {String? sha256}) =>
      !live || _disposed || _failed
      ? Future.value(false)
      : _updates.update(next, sha256);

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
    _finishCapture();
    _restoration?.complete(false);
    _restoration = null;
    _timeout?.cancel();
    _updates.close();
    if (!_initialized.isCompleted) _initialized.complete();
    unawaited(_release());
    super.dispose();
  }
}
