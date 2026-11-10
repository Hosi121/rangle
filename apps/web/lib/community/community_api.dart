import 'dart:convert';
import 'dart:math';

import 'package:http/http.dart' as http;

import '../data/api_clip_repository.dart';
import '../data/clip_repository.dart';

class AccountUser {
  const AccountUser(this.id, this.name) : isAdmin = false;
  const AccountUser._fromResponse(this.id, this.name, this.isAdmin);
  final String id, name;
  // A server-provided display hint. Authorization is enforced by the API;
  // browser state, including this value, must never grant management access.
  final bool isAdmin;
  factory AccountUser.fromJson(Map<String, dynamic> data) =>
      AccountUser._fromResponse(
        data['id'] as String,
        data['name'] as String,
        data['is_admin'] == true,
      );
}

class Engagement {
  const Engagement({this.likes = 0, this.comments = 0, this.liked = false});
  final int likes, comments;
  final bool liked;
  factory Engagement.fromJson(Map<String, dynamic> data) => Engagement(
    likes: data['likes'] as int,
    comments: data['comments'] as int,
    liked: data['liked'] as bool,
  );
}

class ClipComment {
  ClipComment.fromJson(Map<String, dynamic> data)
    : id = data['id'] as String,
      author = AccountUser.fromJson(data['author'] as Map<String, dynamic>),
      text = data['text'] as String,
      createdAt = DateTime.parse(data['created_at'] as String);
  final String id, text;
  final AccountUser author;
  final DateTime createdAt;
}

class CommentPage {
  const CommentPage(this.comments, this.nextCursor);
  final List<ClipComment> comments;
  final String nextCursor;
}

class CheeredClip {
  CheeredClip.fromJson(Map<String, dynamic> data)
    : id = (data['clip'] as Map<String, dynamic>)['id'] as String,
      title = (data['clip'] as Map<String, dynamic>)['title'] as String,
      sport = (data['clip'] as Map<String, dynamic>)['sport'] as String,
      lastCheeredAt = DateTime.parse(data['last_cheered_at'] as String),
      deleted = data['deleted'] as bool? ?? false;
  final String id, title, sport;
  final DateTime lastCheeredAt;
  final bool deleted;
}

class CheerHistoryPage {
  const CheerHistoryPage(this.clips, this.nextCursor);
  final List<CheeredClip> clips;
  final String nextCursor;
}

class CommunityFailure implements Exception {
  const CommunityFailure(this.code);
  final String code;
  String get message => switch (code) {
    'invalid_credentials' => 'IDまたはパスワードが正しくありません。',
    'username_unavailable' => 'このユーザーIDは使用されています。',
    'invalid_account' => 'ユーザーIDとパスワードの入力条件を確認してください。',
    'not_authenticated' => 'ログインしてから、もう一度お試しください。',
    'rate_limited' => '少し時間をおいてから、もう一度お試しください。',
    'invalid_comment' => 'コメントは1〜500文字で入力してください。',
    'forbidden' => 'この操作はできません。',
    'service_unavailable' => '現在この機能を利用できません。',
    'publisher_not_found' => 'この団体は現在表示できません。',
    _ => '接続を確認して、もう一度お試しください。',
  };
}

String communityMessage(Object error) =>
    error is CommunityFailure ? error.message : '接続を確認して、もう一度お試しください。';

/// Requests stay on the application origin. Session cookies are HttpOnly and
/// handled by the browser; Dart never stores passwords or bearer tokens.
class CommunityApi {
  CommunityApi({required this.endpoint, http.Client? client})
    : _client = client ?? http.Client(),
      _ownsClient = client == null;
  final Uri endpoint;
  final http.Client _client;
  final bool _ownsClient;

  Future<Map<String, dynamic>> request(
    String method,
    String path, [
    Map<String, Object?>? body,
  ]) async {
    final message = http.Request(method, endpoint.resolve(path));
    message.headers['Accept'] = 'application/json';
    if (body != null) {
      message.headers['Content-Type'] = 'application/json';
      message.body = jsonEncode(body);
    }
    final response = await http.Response.fromStream(
      await _client.send(message).timeout(const Duration(seconds: 10)),
    ).timeout(const Duration(seconds: 10));
    Map<String, dynamic> data = {};
    if (response.bodyBytes.isNotEmpty) {
      try {
        data =
            jsonDecode(utf8.decode(response.bodyBytes)) as Map<String, dynamic>;
      } catch (_) {
        throw const CommunityFailure('service_unavailable');
      }
    }
    if (response.statusCode < 200 || response.statusCode >= 300) {
      throw CommunityFailure(data['error'] as String? ?? 'request_failed');
    }
    return data;
  }

  Future<Map<String, dynamic>> session() => request('GET', 'auth/me');

  Future<ClipPage> publisherClips(String id, {String? cursor}) =>
      ApiClipRepository(
        endpoint: endpoint.resolve('clips').toString(),
        publisherId: id,
        client: _client,
      ).loadPage(cursor: cursor);
  Future<AccountUser> login(String id, String password) async =>
      AccountUser.fromJson(
        (await request('POST', 'auth/login', {
              'username': id,
              'password': password,
            }))['user']
            as Map<String, dynamic>,
      );
  Future<AccountUser> register(String id, String name, String password) async =>
      AccountUser.fromJson(
        (await request('POST', 'auth/register', {
              'username': id,
              'name': name,
              'password': password,
            }))['user']
            as Map<String, dynamic>,
      );
  Future<void> logout() async {
    await request('POST', 'auth/logout');
  }

  Future<CheerHistoryPage> cheeredClips({String cursor = ''}) async {
    final url = Uri(
      path: 'me/cheered-clips',
      queryParameters: cursor.isEmpty ? null : {'cursor': cursor},
    );
    final data = await request('GET', url.toString());
    final rows = data['clips'], next = data['next_cursor'];
    if (rows is! List ||
        rows.length > 20 ||
        next is! String ||
        next.length > 256) {
      throw const FormatException('Invalid cheer history.');
    }
    return CheerHistoryPage(
      List.unmodifiable(
        rows.map((row) => CheeredClip.fromJson(row as Map<String, dynamic>)),
      ),
      next,
    );
  }

  Future<Engagement> engagement(String clip) async => Engagement.fromJson(
    await request('GET', 'clips/${Uri.encodeComponent(clip)}/engagement'),
  );
  Future<void> like(String clip, bool value) async {
    await request(
      value ? 'PUT' : 'DELETE',
      'clips/${Uri.encodeComponent(clip)}/likes',
    );
  }

  Future<CommentPage> comments(String clip, {String cursor = ''}) async {
    final url = Uri(
      path: 'clips/$clip/comments',
      queryParameters: cursor.isEmpty ? null : {'cursor': cursor},
    );
    final data = await request('GET', url.toString());
    return CommentPage(
      (data['comments'] as List)
          .map((row) => ClipComment.fromJson(row as Map<String, dynamic>))
          .toList(),
      data['next_cursor'] as String,
    );
  }

  Future<ClipComment> comment(
    String clip,
    String text,
    String requestId,
  ) async => ClipComment.fromJson(
    await request('POST', 'clips/${Uri.encodeComponent(clip)}/comments', {
      'text': text,
      'request_id': requestId,
    }),
  );
  Future<void> deleteComment(String clip, String id) async {
    await request(
      'DELETE',
      'clips/${Uri.encodeComponent(clip)}/comments/${Uri.encodeComponent(id)}',
    );
  }

  void close() {
    if (_ownsClient) _client.close();
  }

  static String requestId() {
    final random = Random.secure();
    return List.generate(
      16,
      (_) => random.nextInt(256).toRadixString(16).padLeft(2, '0'),
    ).join();
  }
}
