import 'package:http/http.dart' as http;

import '../community/community_api.dart';
import '../models/publisher_account.dart';

bool _validId(String id) => RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(id);
String _id(String id) {
  if (!_validId(id)) throw const FormatException('Invalid account-local ID.');
  return id;
}

class PublisherStaff {
  PublisherStaff.fromJson(Map<String, dynamic> data)
    : id = data['id'] as String,
      username = data['username'] as String,
      role = data['role'] as String,
      disabled = data['disabled'] as bool {
    if (!_validId(id) ||
        !RegExp(r'^[a-z0-9_-]{3,32}$').hasMatch(username) ||
        !const ['administrator', 'editor'].contains(role)) {
      throw const FormatException('Invalid staff profile.');
    }
  }
  final String id, username, role;
  final bool disabled;
  String get roleLabel => role == 'administrator' ? '団体管理者' : '投稿担当';
}

class PublisherIdentity {
  PublisherIdentity.fromJson(Map<String, dynamic> data)
    : account = PublisherAccount.fromJson(
        data['account'] as Map<String, dynamic>,
      ),
      staff = PublisherStaff.fromJson(data['staff'] as Map<String, dynamic>) {
    if (staff.disabled) throw const FormatException('Disabled staff session.');
  }
  final PublisherAccount account;
  final PublisherStaff staff;
  bool get canManageStaff => staff.role == 'administrator';
}

class PublisherEntry {
  PublisherEntry.fromJson(Map<String, dynamic> data)
    : account = PublisherAccount.fromJson(
        data['account'] as Map<String, dynamic>,
      ),
      status = data['status'] as String,
      deletionRequested = data['deletion_requested'] as bool? ?? false {
    if (!const [
      'active',
      'suspended',
      'deleting',
      'deleted',
    ].contains(status)) {
      throw const FormatException('Invalid publisher lifecycle.');
    }
  }
  final PublisherAccount account;
  final String status;
  final bool deletionRequested;
  bool get manageable => status == 'active' || status == 'suspended';
  String get statusLabel => switch (status) {
    'active' => '利用中',
    'suspended' => '利用停止中',
    'deleting' => '削除中',
    _ => '削除済み',
  };
}

class PublisherPage {
  PublisherPage(this.entries, this.nextCursor);
  final List<PublisherEntry> entries;
  final String nextCursor;
}

class PublisherDeletion {
  PublisherDeletion.fromJson(Map<String, dynamic> data)
    : state = data['state'] as String,
      stage = data['stage'] as String? ?? '',
      requested = data['requested'] as bool? ?? false,
      prefix = data['prefix'] as int? ?? 0,
      total = data['prefix_total'] as int? ?? 0 {
    if (!const ['active', 'suspended', 'deleting', 'deleted'].contains(state) ||
        !const ['', 'jobs', 'objects', 'cache', 'complete'].contains(stage) ||
        prefix < 0 ||
        total < prefix ||
        total > 30 ||
        state == 'deleted' && (stage != 'complete' || prefix != total) ||
        state == 'deleting' &&
            !const ['jobs', 'objects', 'cache'].contains(stage) ||
        const ['active', 'suspended'].contains(state) && stage.isNotEmpty) {
      throw const FormatException('Invalid deletion progress.');
    }
  }
  final String state, stage;
  final bool requested;
  final int prefix, total;
  bool get pending => state == 'deleting';
  String get label => switch (stage) {
    'jobs' => '生成の終了を確認しています',
    'objects' => '動画・モデルを削除しています',
    'cache' => '配信キャッシュを削除しています',
    'complete' => '団体を削除しました',
    _ => '団体の削除',
  };
}

class StaffPage {
  StaffPage(this.staff, this.nextCursor);
  final List<PublisherStaff> staff;
  final String nextCursor;
}

class PublisherInput {
  PublisherInput.fromJson(Map<String, dynamic> data)
    : id = data['id'] as String,
      title = data['title'] as String,
      mode = data['mode'] as String,
      enabled = data['enabled'] as bool {
    if (!RegExp(r'^[a-z0-9][a-z0-9-]{0,62}$').hasMatch(id) ||
        title.isEmpty ||
        !const ['static', 'dynamic', 'live'].contains(mode)) {
      throw const FormatException('Invalid reviewed input.');
    }
  }
  final String id, title, mode;
  final bool enabled;
  String get modeLabel => switch (mode) {
    'dynamic' => '動く自由視点',
    'live' => 'ライブ生成',
    _ => '静止した自由視点',
  };
}

String publisherMessage(Object error) {
  if (error is CommunityFailure) {
    return switch (error.code) {
      'invalid_password' => 'パスワードは15〜128文字で入力してください。',
      'invalid_credentials' => '団体ID・担当者名・パスワードを確認してください。',
      'invalid_staff' => '担当者名・パスワード・役割を確認してください。',
      'staff_exists' => 'この担当者名は使用されています。',
      'publisher_exists' => 'この団体IDは使用されています。',
      'invalid_publisher' => '団体の入力内容を確認してください。',
      'publisher_access_denied' ||
      'operator_required' ||
      'admin_required' => 'この操作を行う権限がありません。',
      'credentials_changed' => '資格情報が変更されました。ログインし直してください。',
      'not_found' => '対象が見つかりません。再読み込みしてください。',
      _ => communityMessage(error),
    };
  }
  return '接続を確認して、もう一度お試しください。';
}

// Only server-issued HttpOnly cookies authenticate these requests. No password
// or role is stored in browser storage or shared with the viewer account.
class PublisherApi {
  PublisherApi({required this.endpoint, http.Client? client})
    : _transport = CommunityApi(endpoint: endpoint, client: client);
  final Uri endpoint;
  final CommunityApi _transport;
  Future<Map<String, dynamic>> _request(
    String method,
    String path, [
    Map<String, Object?>? body,
  ]) => _transport.request(method, path, body);
  void close() => _transport.close();

  Future<Map<String, dynamic>> staffSession() =>
      _request('GET', 'publisher-auth/me');
  Future<PublisherIdentity> staffLogin(
    String account,
    String username,
    String password,
  ) async => PublisherIdentity.fromJson(
    (await _request('POST', 'publisher-auth/login', {
          'account_id': account,
          'username': username,
          'password': password,
        }))['identity']
        as Map<String, dynamic>,
  );
  Future<void> staffLogout() async {
    await _request('POST', 'publisher-auth/logout');
  }

  Future<void> changePassword(String current, String password) async {
    await _request('PUT', 'publisher-auth/password', {
      'current_password': current,
      'new_password': password,
    });
  }

  String _staffRoot(String account, bool operator) =>
      '${operator ? 'operations/' : ''}publishers/${_id(account)}/staff';
  Future<StaffPage> staff(
    String account, {
    bool operator = false,
    String cursor = '',
  }) async {
    final path = Uri(
      path: _staffRoot(account, operator),
      queryParameters: cursor.isEmpty ? null : {'cursor': cursor},
    );
    final data = await _request('GET', path.toString());
    final rows = data['staff'] as List, next = data['next_cursor'] as String;
    if (rows.length > 20 || next.length > 256) {
      throw const FormatException('Invalid staff page.');
    }
    return StaffPage(
      rows
          .map((row) => PublisherStaff.fromJson(row as Map<String, dynamic>))
          .toList(),
      next,
    );
  }

  Future<void> createStaff(
    String account,
    String username,
    String password,
    String role, {
    bool operator = false,
  }) async {
    await _request('POST', _staffRoot(account, operator), {
      'username': username,
      'password': password,
      'role': role,
    });
  }

  Future<void> updateStaff(
    String account,
    String staff,
    String role,
    bool disabled, {
    bool operator = false,
  }) async {
    await _request('PUT', '${_staffRoot(account, operator)}/${_id(staff)}', {
      'role': role,
      'disabled': disabled,
    });
  }

  Future<void> resetPassword(
    String account,
    String staff,
    String password, {
    bool operator = false,
  }) async {
    await _request(
      'PUT',
      '${_staffRoot(account, operator)}/${_id(staff)}/password',
      {'password': password},
    );
  }

  Future<PublisherPage> publishers({String cursor = ''}) async {
    final path = Uri(
      path: 'operations/publishers',
      queryParameters: cursor.isEmpty ? null : {'cursor': cursor},
    );
    final data = await _request('GET', path.toString());
    final rows = data['publishers'] as List,
        next = data['next_cursor'] as String;
    if (rows.length > 20 || next.length > 64) {
      throw const FormatException('Invalid publisher page.');
    }
    return PublisherPage(
      rows
          .map((row) => PublisherEntry.fromJson(row as Map<String, dynamic>))
          .toList(),
      next,
    );
  }

  Future<PublisherAccount> provision({
    required String id,
    required String name,
    required String kind,
    required String username,
    required String password,
  }) async {
    final data = await _request('POST', 'operations/publishers', {
      'id': id,
      'name': name,
      'kind': kind,
      'staff_username': username,
      'staff_password': password,
    });
    return PublisherAccount.fromJson(data['account'] as Map<String, dynamic>);
  }

  String _inputRoot(String account) =>
      'operations/publishers/${_id(account)}/generation/presets';

  Future<bool> deletionRequested(String account) async =>
      (await _request(
            'GET',
            'publishers/${_id(account)}/deletion-request',
          ))['requested']
          as bool;

  Future<bool> requestDeletion(String account, bool requested) async =>
      (await _request('PUT', 'publishers/${_id(account)}/deletion-request', {
            'requested': requested,
            'confirmation': account,
          }))['requested']
          as bool;

  Future<PublisherDeletion> deletionStatus(String account) async =>
      PublisherDeletion.fromJson(
        await _request('GET', 'operations/publishers/${_id(account)}/deletion'),
      );

  Future<PublisherDeletion> deletePublisher(String account) async =>
      PublisherDeletion.fromJson(
        await _request(
          'DELETE',
          'operations/publishers/${_id(account)}/deletion',
          {'confirmation': account},
        ),
      );
  Future<List<PublisherInput>> inputs(String account) async =>
      _inputs(await _request('GET', _inputRoot(account)));
  Future<List<PublisherInput>> assignInputs(
    String account,
    Set<String> presets,
  ) async => _inputs(
    await _request('PUT', _inputRoot(account), {
      'presets': presets.toList()..sort(),
    }),
  );
  List<PublisherInput> _inputs(Map<String, dynamic> data) {
    final rows = data['presets'] as List;
    if (rows.length > 10) {
      throw const FormatException('Too many reviewed inputs.');
    }
    final result = rows
        .map((row) => PublisherInput.fromJson(row as Map<String, dynamic>))
        .toList();
    if (result.map((row) => row.id).toSet().length != result.length) {
      throw const FormatException('Duplicate reviewed input.');
    }
    return result;
  }
}
