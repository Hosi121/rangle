import 'dart:convert';
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/community/community_api.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/sharing/share_clip.dart';

import 'support/fake_viewer.dart';

class CommunityFixture {
  Map<String, String>? user;
  bool liked = false, rejectLogin = false, loseFirstCommentResponse = false;
  List<Map<String, Object?>> history = [];
  bool historyFails = false;
  final cheerBins = List.filled(48, 0);
  final cheerReceipts = <String>{};
  Future<http.Response>? delayedCheers;
  final comments = <String, Map<String, Object?>>{};
  final posts = <Map<String, dynamic>>[];
  Future<http.Response>? delayedEngagement;
  late final client = MockClient((request) async {
    final path = request.url.path;
    http.Response reply(Object value, [int status = 200]) =>
        http.Response.bytes(utf8.encode(jsonEncode(value)), status);
    if (path.endsWith('/auth/me')) {
      return reply({'user': user, 'available': true});
    }
    if (path.endsWith('/auth/login') || path.endsWith('/auth/register')) {
      if (rejectLogin) return reply({'error': 'invalid_credentials'}, 401);
      final body = jsonDecode(request.body) as Map<String, dynamic>;
      user = {
        'id': body['username'] as String,
        'name': (body['name'] as String?)?.isNotEmpty == true
            ? body['name'] as String
            : 'テストユーザー',
      };
      return reply({'user': user});
    }
    if (path.endsWith('/auth/logout')) {
      user = null;
      return http.Response('', 204);
    }
    if (path.endsWith('/me/cheered-clips')) {
      if (historyFails) return reply({'error': 'service_unavailable'}, 503);
      return reply({'clips': history, 'next_cursor': ''});
    }
    if (path.endsWith('/cheers')) {
      if (request.method == 'POST') {
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        if (cheerReceipts.add(body['request_id'] as String)) {
          final bins = (body['bins'] as List).cast<int>();
          for (var i = 0; i < 48; i++) {
            cheerBins[i] += bins[i];
          }
        }
      } else if (delayedCheers != null) {
        final delayed = delayedCheers!;
        delayedCheers = null;
        return delayed;
      }
      return reply({'bins': cheerBins});
    }
    if (path.endsWith('/engagement')) {
      final delayed = delayedEngagement;
      delayedEngagement = null;
      if (delayed != null) return delayed;
      return reply({
        'likes': liked ? 1 : 0,
        'comments': comments.length,
        'liked': liked,
      });
    }
    if (path.endsWith('/likes')) {
      liked = request.method == 'PUT';
      return http.Response('', 204);
    }
    if (path.endsWith('/comments') && request.method == 'GET') {
      return reply({'comments': comments.values.toList(), 'next_cursor': ''});
    }
    if (path.endsWith('/comments') && request.method == 'POST') {
      final body = jsonDecode(request.body) as Map<String, dynamic>;
      posts.add(body);
      final id = body['request_id'] as String;
      final row = comments.putIfAbsent(
        id,
        () => {
          'id': id,
          'author': user,
          'text': body['text'],
          'created_at': '2026-11-09T00:00:00Z',
        },
      );
      if (loseFirstCommentResponse) {
        loseFirstCommentResponse = false;
        return reply({'error': 'service_unavailable'}, 503);
      }
      return reply(row, 201);
    }
    if (path.contains('/comments/') && request.method == 'DELETE') {
      comments.remove(path.split('/').last);
      return http.Response('', 204);
    }
    throw StateError('Unexpected request: ${request.method} $path');
  });
  CommunityApi get api => CommunityApi(
    endpoint: Uri.parse('https://rangle.test/api/v1/'),
    client: client,
  );
}

Future<void> pumpCommunity(
  WidgetTester tester,
  CommunityFixture fixture, {
  void Function()? videoCreated,
}) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  addTearDown(fixture.client.close);
  await tester.pumpWidget(
    RangleApp(
      repository: const DemoClipRepository(),
      community: fixture.api,
      viewerFactory: (_) {
        videoCreated?.call();
        return FakeViewer();
      },
      sceneFactory: (_) => FakeViewer(),
    ),
  );
  await tester.pumpAndSettle();
}

Future<void> login(WidgetTester tester) async {
  await tester.enterText(find.byKey(const Key('account-username')), 'viewer');
  await tester.enterText(
    find.byKey(const Key('account-password')),
    'a long test password 2026',
  );
  await tester.tap(find.widgetWithText(FilledButton, 'ログインする'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('account history opens the selected clip in the feed', (
    tester,
  ) async {
    final fixture = CommunityFixture()
      ..user = {'id': 'viewer', 'name': 'Viewer'}
      ..history = [
        {
          'clip': {'id': 'demo-03', 'title': '応援した動画', 'sport': 'バスケットボール'},
          'last_cheered_at': '2026-11-10T00:00:00Z',
        },
      ];
    await pumpCommunity(tester, fixture);
    await tester.tap(find.byTooltip('アカウント'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('声援を送った動画'));
    await tester.pumpAndSettle();
    expect(find.text('応援した動画'), findsOneWidget);
    await tester.tap(find.text('応援した動画'));
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 03'), findsOneWidget);
    expect(find.text('声援を送った動画'), findsNothing);
    expect(tester.takeException(), isNull);
  });
  testWidgets('a removed history clip leaves the current feed usable', (
    tester,
  ) async {
    final fixture = CommunityFixture()
      ..user = {'id': 'viewer', 'name': 'Viewer'}
      ..history = [
        {
          'clip': {'id': 'removed', 'title': '公開終了', 'sport': 'バスケットボール'},
          'last_cheered_at': '2026-11-10T00:00:00Z',
        },
      ];
    await pumpCommunity(tester, fixture);
    await tester.tap(find.byTooltip('アカウント'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('声援を送った動画'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('公開終了'));
    await tester.pumpAndSettle();
    expect(find.byTooltip('検索'), findsOneWidget);
    expect(find.text('VRU Basketball'), findsOneWidget);
    expect(find.textContaining('この動画を開けませんでした'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('late anonymous cheer totals cannot overwrite local applause', (
    tester,
  ) async {
    final initial = Completer<http.Response>();
    final fixture = CommunityFixture()..delayedCheers = initial.future;
    await pumpCommunity(tester, fixture);
    await tester.tap(find.byTooltip('声援を送る（何度でも押せます）'));
    await tester.pumpAndSettle();
    await login(tester);
    await tester.tap(find.byTooltip('声援を送る（何度でも押せます）'));
    await tester.pump(const Duration(seconds: 2));
    await tester.pumpAndSettle();
    expect(fixture.cheerBins.reduce((a, b) => a + b), 2);
    initial.complete(
      http.Response(jsonEncode({'bins': List.filled(48, 0)}), 200),
    );
    await tester.pumpAndSettle();
    expect(find.text('2'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
  });
  testWidgets(
    'registration validates passwords and exposes only the public account',
    (tester) async {
      final fixture = CommunityFixture();
      await pumpCommunity(tester, fixture);
      await tester.tap(find.byTooltip('ログイン'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, 'アカウントを作成'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const Key('account-username')),
        'viewer',
      );
      await tester.enterText(
        find.byKey(const Key('account-password')),
        'short',
      );
      await tester.tap(find.widgetWithText(FilledButton, '登録する'));
      await tester.pumpAndSettle();
      expect(find.text('15〜128文字で入力してください。'), findsOneWidget);
      expect(fixture.user, isNull);
      await tester.enterText(
        find.byKey(const Key('account-password')),
        'a long test password 2026',
      );
      await tester.enterText(
        find.byKey(const Key('account-confirm-password')),
        'a long test password 2026',
      );
      await tester.tap(find.widgetWithText(FilledButton, '登録する'));
      await tester.pumpAndSettle();
      expect(find.byTooltip('アカウント'), findsOneWidget);
      await tester.tap(find.byTooltip('アカウント'));
      await tester.pumpAndSettle();
      expect(find.text('@viewer'), findsOneWidget);
      await tester.tap(find.widgetWithText(OutlinedButton, 'ログアウト'));
      await tester.pumpAndSettle();
      expect(find.byTooltip('ログイン'), findsOneWidget);
      expect(fixture.user, isNull);
    },
  );
  testWidgets(
    'repeatable cheering asks for login and retains the active viewer',
    (tester) async {
      var videos = 0;
      final fixture = CommunityFixture();
      await pumpCommunity(tester, fixture, videoCreated: () => videos++);
      await tester.tap(find.byTooltip('声援を送る（何度でも押せます）'));
      await tester.pumpAndSettle();
      await login(tester);
      for (var i = 0; i < 3; i++) {
        await tester.tap(find.byTooltip('声援を送る（何度でも押せます）'));
        await tester.pump();
      }
      expect(videos, 1);
      await tester.pump(const Duration(seconds: 2));
      await tester.pumpAndSettle();
      expect(fixture.cheerBins.reduce((a, b) => a + b), 4);
      expect(find.text('4'), findsOneWidget);
      expect(find.byIcon(Icons.favorite), findsNothing);
      await tester.pumpWidget(const SizedBox());
    },
  );
  testWidgets('bad credentials keep the form open and show a useful error', (
    tester,
  ) async {
    final fixture = CommunityFixture()..rejectLogin = true;
    await pumpCommunity(tester, fixture);
    await tester.tap(find.byTooltip('ログイン'));
    await tester.pumpAndSettle();
    await login(tester);
    expect(find.text('IDまたはパスワードが正しくありません。'), findsOneWidget);
    expect(find.byKey(const Key('account-password')), findsOneWidget);
  });
  testWidgets(
    'retrying a comment keeps its request identity and can delete the result',
    (tester) async {
      final fixture = CommunityFixture()
        ..user = {'id': 'viewer', 'name': 'テスト'}
        ..loseFirstCommentResponse = true;
      await pumpCommunity(tester, fixture);
      await tester.tap(find.byTooltip('概要'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('コメントを見る'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byType(TextField),
        'いい角度！ <script>plain text</script>',
      );
      await tester.pump();
      await tester.tap(find.byTooltip('コメントを投稿'));
      await tester.pumpAndSettle();
      expect(find.text('現在この機能を利用できません。'), findsOneWidget);
      await tester.tap(find.byTooltip('コメントを投稿'));
      await tester.pumpAndSettle();
      expect(fixture.posts, hasLength(2));
      expect(fixture.posts[0]['request_id'], fixture.posts[1]['request_id']);
      expect(fixture.comments, hasLength(1));
      expect(find.text('いい角度！ <script>plain text</script>'), findsOneWidget);
      await tester.tap(find.byTooltip('自分のコメントを削除'));
      await tester.pumpAndSettle();
      expect(fixture.comments, isEmpty);
    },
  );
  testWidgets('hashtag and keyword search can return to the complete feed', (
    tester,
  ) async {
    int? clipCount() => tester
        .widget<PageView>(find.byKey(const Key('highlight-feed')))
        .childrenDelegate
        .estimatedChildCount;
    await tester.pumpWidget(
      RangleApp(
        repository: const DemoClipRepository(),
        viewerFactory: (_) => FakeViewer(),
        sceneFactory: (_) => FakeViewer(),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('検索'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField), '#自由視点');
    await tester.tap(find.widgetWithText(FilledButton, '検索'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('highlight-feed')), findsNothing);
    await tester.tap(find.byKey(const ValueKey('search-result-demo-01')));
    await tester.pumpAndSettle();
    expect(clipCount(), 1);
    await tester.tap(find.byTooltip('絞り込み解除'));
    await tester.pumpAndSettle();
    expect(clipCount(), 3);
    await tester.tap(find.byTooltip('検索'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField), '存在しないキーワード');
    await tester.tap(find.widgetWithText(FilledButton, '検索'));
    await tester.pumpAndSettle();
    expect(find.text('動画が見つかりませんでした。'), findsOneWidget);
    await tester.tap(find.byType(BackButton));
    await tester.pumpAndSettle();
    expect(clipCount(), 3);
  });
  test(
    'shared links point to the selected clip and clear unrelated filters',
    () {
      final url = clipLink(
        Uri.parse('https://app.rangleplay.stream/demo/?q=old#section'),
        'basketball',
      );
      expect(
        url.toString(),
        'https://app.rangleplay.stream/demo/?clip=basketball',
      );
    },
  );
}
