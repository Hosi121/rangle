import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';

import 'community_api.dart';

const cheerBinCount = 48;

/// Optimistic taps are separate from accepted totals. Retries retain the batch
/// ID, so a lost response cannot count the same applause twice.
class CheerController extends ChangeNotifier {
  CheerController(this.api, this.clip, {this.onError});
  final CommunityApi? api;
  final String clip;
  final ValueChanged<Object>? onError;
  List<int> _server = List.filled(cheerBinCount, 0);
  final List<int> _pending = List.filled(cheerBinCount, 0);
  List<int>? _flight;
  String? _requestId;
  Timer? _timer;
  bool _sending = false, _disposed = false;
  bool _closing = false;
  Completer<void>? _completion;
  int _retries = 0;
  int _revision = 0;
  bool get hasPending => _flight != null || _pending.any((n) => n > 0);
  List<int> get bins => List.generate(
    cheerBinCount,
    (i) => _server[i] + _pending[i] + (_flight?[i] ?? 0),
  );
  int get total => bins.fold(0, (a, b) => a + b);

  Future<void> load() async {
    if (api == null || _sending) return;
    final revision = _revision;
    try {
      final value = await api!.request(
        'GET',
        'clips/${Uri.encodeComponent(clip)}/cheers',
      );
      if (!_disposed && !hasPending && revision == _revision) {
        _server = _parse(value);
        notifyListeners();
      }
    } catch (_) {
      /* Media and playback do not depend on community storage. */
    }
  }

  static List<int> _parse(Map<String, dynamic> value) {
    final bins = value['bins'];
    if (bins is! List ||
        bins.length != cheerBinCount ||
        bins.any((n) => n is! int || n < 0)) {
      throw const CommunityFailure('service_unavailable');
    }
    return bins.cast<int>().toList();
  }

  bool cheer(Duration position, Duration duration) {
    if (api == null || duration <= Duration.zero || _disposed || _closing) {
      return false;
    }
    if (_pending.fold(0, (a, b) => a + b) >= 256) return false;
    final bin =
        (position.inMicroseconds / duration.inMicroseconds * cheerBinCount)
            .floor()
            .clamp(0, cheerBinCount - 1);
    _pending[bin]++;
    _revision++;
    _retries = 0;
    notifyListeners();
    _schedule();
    return true;
  }

  void _schedule([Duration delay = const Duration(seconds: 2)]) {
    if (_disposed || _timer != null || _sending || !hasPending) return;
    _timer = Timer(delay, () {
      _timer = null;
      unawaited(flush());
    });
  }

  Future<void> flush() async {
    if (_disposed || _sending || api == null || !hasPending) return;
    _timer?.cancel();
    _timer = null;
    if (_flight == null) {
      var remaining = 64;
      _flight = List.generate(cheerBinCount, (i) {
        final n = math.min(remaining, _pending[i]);
        _pending[i] -= n;
        remaining -= n;
        return n;
      });
      _requestId = CommunityApi.requestId();
    }
    _sending = true;
    _completion = Completer<void>();
    try {
      final response = await api!.request(
        'POST',
        'clips/${Uri.encodeComponent(clip)}/cheers',
        {'request_id': _requestId, 'bins': _flight},
      );
      if (_disposed) return;
      _server = _parse(response);
      _revision++;
      _flight = null;
      _requestId = null;
      _retries = 0;
      notifyListeners();
    } catch (error) {
      if (_disposed) return;
      _retries++;
      onError?.call(error);
      if (!_disposed) notifyListeners();
    } finally {
      _sending = false;
      _completion?.complete();
      if (!_disposed && _retries < 3) _schedule(const Duration(seconds: 3));
    }
  }

  @override
  void dispose() {
    if (_disposed) return;
    _disposed = true;
    _timer?.cancel();
    super.dispose();
  }

  /// Finish an ordinary card eviction while the account is unchanged.
  /// Sign-out disposes immediately, so a new user cannot inherit a batch.
  Future<void> finish() async {
    _closing = true;
    _timer?.cancel();
    _timer = null;
    if (_sending) await _completion?.future;
    while (!_disposed && hasPending) {
      await flush();
      if (_retries > 0) break;
    }
    dispose();
  }
}
