import 'dart:async';

/// One active replacement and the latest waiting metadata. A completion means
/// the iframe has drawn that revision, so newer metadata cannot starve it.
class LiveSourceUpdates {
  LiveSourceUpdates({
    required this.initialSource,
    required this.initialSha256,
    required this.start,
    required this.cancel,
    this.timeout = const Duration(seconds: 45),
  });

  final Uri initialSource;
  final String? initialSha256;
  final void Function(LiveSourceUpdate) start;
  final void Function() cancel;
  final Duration timeout;
  LiveSourceUpdate? active, _pending;
  Uri? _displayed;
  String? _displayedSha256;
  Timer? _timer;
  int _sequence = 0;
  bool _initialized = false, _visible = true, _closed = false;

  Future<bool> update(Uri source, String? sha256) {
    if (_closed || !_visible) return Future.value(false);
    for (final request in [active, _pending]) {
      if (request?.source == source && request?.sha256 == sha256) {
        return request!._completion.future;
      }
    }
    if (active == null && _displayed == source && _displayedSha256 == sha256) {
      return Future.value(true);
    }
    final request = LiveSourceUpdate(++_sequence, source, sha256);
    _pending?._completion.complete(false);
    _pending = request;
    _drain();
    return request._completion.future;
  }

  void initialized() {
    if (_closed || _initialized) return;
    _initialized = true;
    _displayed = initialSource;
    _displayedSha256 = initialSha256;
    _drain();
  }

  bool matches(Object? id, Object? url) =>
      id is int &&
      url is String &&
      active != null &&
      active!.id == id &&
      active!.source.toString() == url;

  void finish(Object? id, Object? url, bool ready) {
    if (!matches(id, url)) return;
    final request = active!;
    _timer?.cancel();
    active = null;
    if (ready) {
      _displayed = request.source;
      _displayedSha256 = request.sha256;
    }
    request._completion.complete(ready);
    _drain();
  }

  void _drain() {
    if (_closed || !_initialized || !_visible || active != null) return;
    final request = _pending;
    if (request == null) return;
    _pending = null;
    if (_displayed == request.source && _displayedSha256 == request.sha256) {
      request._completion.complete(true);
      return;
    }
    active = request;
    _timer = Timer(timeout, () {
      if (!identical(active, request)) return;
      cancel();
      finish(request.id, request.source.toString(), false);
    });
    start(request);
  }

  void visible(bool value) {
    _visible = value;
    if (!value) _clear();
  }

  void _clear() {
    _timer?.cancel();
    final request = active;
    active = null;
    _pending?._completion.complete(false);
    _pending = null;
    if (request != null) {
      cancel();
      request._completion.complete(false);
    }
  }

  void close() {
    _closed = true;
    _clear();
  }
}

class LiveSourceUpdate {
  LiveSourceUpdate(this.id, this.source, this.sha256);
  final int id;
  final Uri source;
  final String? sha256;
  final _completion = Completer<bool>();
}
