import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/community/account_controller.dart';
import 'package:rangle_web/community/cheer_history_dialog.dart';
import 'package:rangle_web/community/community_api.dart';

Map<String, Object> row(String id) => {
  'clip': {'id': id, 'title': 'Video $id', 'sport': 'basketball'},
  'last_cheered_at': '2026-11-10T00:00:00Z',
};
http.Response reply(Object data, [int status = 200]) =>
    http.Response(jsonEncode(data), status);
void main() {
  testWidgets('deleted video keeps its history row without a playback action', (
    tester,
  ) async {
    final client = MockClient(
      (_) async => reply({
        'clips': [
          {...row('deleted'), 'deleted': true},
        ],
        'next_cursor': '',
      }),
    );
    final account = AccountController(
      CommunityApi(
        endpoint: Uri.parse('https://example.test/api/v1/'),
        client: client,
      ),
    )..user = const AccountUser('viewer', 'Viewer');
    addTearDown(account.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: CheerHistoryDialog(account: account)),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('削除済み動画'), findsOneWidget);
    final tile = tester.widget<ListTile>(find.byType(ListTile));
    expect(tile.enabled, isFalse);
    expect(tile.onTap, isNull);
    expect(find.byIcon(Icons.chevron_right), findsNothing);
  });
  testWidgets('history retries a failed page and continues with its cursor', (
    tester,
  ) async {
    var requests = 0;
    final client = MockClient((request) async {
      requests++;
      if (requests == 1) return reply({'error': 'service_unavailable'}, 503);
      final cursor = request.url.queryParameters['cursor'];
      return reply({
        'clips': [row(cursor == null ? 'first' : 'second')],
        'next_cursor': cursor == null ? 'next' : '',
      });
    });
    addTearDown(client.close);
    final account = AccountController(
      CommunityApi(
        endpoint: Uri.parse('https://example.test/api/v1/'),
        client: client,
      ),
    )..user = const AccountUser('viewer', 'Viewer');
    addTearDown(account.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: CheerHistoryDialog(account: account)),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('現在この機能を利用できません。'), findsOneWidget);
    await tester.tap(find.text('再読み込み'));
    await tester.pumpAndSettle();
    expect(find.text('Video first'), findsOneWidget);
    await tester.tap(find.text('もっと見る'));
    await tester.pumpAndSettle();
    expect(find.text('Video second'), findsOneWidget);
    expect(requests, 3);
  });
  testWidgets('logout hides rows from an in-flight personal history request', (
    tester,
  ) async {
    final pending = Completer<http.Response>();
    final client = MockClient((_) => pending.future);
    addTearDown(client.close);
    final account = AccountController(
      CommunityApi(
        endpoint: Uri.parse('https://example.test/api/v1/'),
        client: client,
      ),
    )..user = const AccountUser('viewer', 'Viewer');
    addTearDown(account.dispose);
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(body: CheerHistoryDialog(account: account)),
      ),
    );
    account.expired();
    await tester.pump();
    pending.complete(
      reply({
        'clips': [row('private')],
        'next_cursor': '',
      }),
    );
    await tester.pumpAndSettle();
    expect(find.text('Video private'), findsNothing);
    expect(find.text('ログインして履歴を確認してください。'), findsOneWidget);
  });
}
