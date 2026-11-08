import 'package:flutter/foundation.dart';

import 'community_api.dart';

class AccountController extends ChangeNotifier {
  AccountController(this.api);
  final CommunityApi api;
  AccountUser? user;
  bool loading = true, available = false;
  bool _disposed = false;
  String? error;

  Future<void> initialize() async {
    loading = true;
    _notify();
    try {
      final data = await api.session();
      if (_disposed) return;
      user = data['user'] == null
          ? null
          : AccountUser.fromJson(data['user'] as Map<String, dynamic>);
      available = data['available'] == true;
      error = null;
    } catch (e) {
      if (_disposed) return;
      error = communityMessage(e);
    } finally {
      if (!_disposed) {
        loading = false;
        _notify();
      }
    }
  }

  Future<void> authenticate(
    String id,
    String name,
    String password, {
    required bool register,
  }) async {
    final account = register
        ? await api.register(id, name, password)
        : await api.login(id, password);
    if (_disposed) return;
    user = account;
    available = true;
    error = null;
    _notify();
  }

  Future<void> logout() async {
    await api.logout();
    if (_disposed) return;
    user = null;
    _notify();
  }

  void expired() {
    user = null;
    _notify();
  }

  void _notify() {
    if (!_disposed) notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    api.close();
    super.dispose();
  }
}
