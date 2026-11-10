import 'package:flutter/foundation.dart';

import 'publisher_api.dart';

class PublisherController extends ChangeNotifier {
  PublisherController(this.api);
  final PublisherApi api;
  PublisherIdentity? identity;
  bool loading = true, available = false;
  String? error;
  int _epoch = 0;
  bool _disposed = false;

  Future<void> initialize() async {
    final epoch = ++_epoch;
    loading = true;
    _notify();
    try {
      final data = await api.staffSession();
      if (_disposed || epoch != _epoch) return;
      identity = data['identity'] == null
          ? null
          : PublisherIdentity.fromJson(
              data['identity'] as Map<String, dynamic>,
            );
      available = data['available'] == true;
      error = null;
    } catch (e) {
      if (!_disposed && epoch == _epoch) {
        identity = null;
        available = false;
        error = publisherMessage(e);
      }
    } finally {
      if (!_disposed && epoch == _epoch) {
        loading = false;
        _notify();
      }
    }
  }

  Future<void> login(String account, String username, String password) async {
    final epoch = ++_epoch;
    try {
      final next = await api.staffLogin(account, username, password);
      if (_disposed || epoch != _epoch) return;
      identity = next;
      available = true;
      error = null;
    } finally {
      if (!_disposed && epoch == _epoch) {
        loading = false;
        _notify();
      }
    }
  }

  Future<void> logout() async {
    final epoch = ++_epoch;
    await api.staffLogout();
    if (_disposed || epoch != _epoch) return;
    identity = null;
    loading = false;
    error = null;
    _notify();
  }

  void expired() {
    _epoch++;
    identity = null;
    loading = false;
    _notify();
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    _epoch++;
    api.close();
    super.dispose();
  }
}
