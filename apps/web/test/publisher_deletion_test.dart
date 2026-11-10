import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/models/publisher_account.dart';
import 'package:rangle_web/publishers/publisher_api.dart';
import 'package:rangle_web/publishers/publisher_deletion_screen.dart';

import 'support/publisher_fixture.dart';

const account = PublisherAccount(
  id: 'club-a',
  name: 'デモ団体',
  kind: 'organization',
);
final endpoint = Uri.parse('https://app.example.test/api/v1/');

void main() {
  test('deletion response rejects inconsistent completion and size bounds', () {
    for (final data in <Map<String, dynamic>>[
      {'state': 'deleted', 'stage': 'objects'},
      {'state': 'deleted', 'stage': 'complete', 'prefix': 0, 'prefix_total': 3},
      {'state': 'deleting', 'stage': '', 'prefix': 0, 'prefix_total': 3},
      {'state': 'deleting', 'stage': 'cache', 'prefix': 0, 'prefix_total': 100},
    ]) {
      expect(() => PublisherDeletion.fromJson(data), throwsFormatException);
    }
  });
  testWidgets(
    'administrator requests and withdraws deletion with exact confirmation',
    (tester) async {
      tester.view.physicalSize = const Size(320, 740);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      var requested = false;
      final writes = <http.Request>[];
      final client = MockClient((r) async {
        expect(r.url.path, '/api/v1/publishers/club-a/deletion-request');
        if (r.method == 'PUT') {
          writes.add(r);
          final body = jsonDecode(r.body) as Map<String, dynamic>;
          expect(body['confirmation'], 'club-a');
          requested = body['requested'] as bool;
        }
        return publisherJson({'requested': requested});
      });
      addTearDown(client.close);
      await tester.pumpWidget(
        MaterialApp(
          home: PublisherDeletionScreen(
            api: PublisherApi(endpoint: endpoint, client: client),
            account: account,
          ),
        ),
      );
      await tester.pumpAndSettle();
      for (final request in [true, false]) {
        await tester.tap(
          find.widgetWithText(OutlinedButton, request ? '削除を依頼' : '削除依頼を取り消す'),
        );
        await tester.pumpAndSettle();
        await tester.enterText(
          find.widgetWithText(TextFormField, '団体ID'),
          'foreign',
        );
        await tester.tap(
          find.widgetWithText(FilledButton, request ? '依頼する' : '依頼を取り消す'),
        );
        await tester.pumpAndSettle();
        expect(find.text('団体IDが一致しません。'), findsOneWidget);
        expect(writes.length, request ? 0 : 1);
        await tester.enterText(
          find.widgetWithText(TextFormField, '団体ID'),
          'club-a',
        );
        await tester.tap(
          find.widgetWithText(FilledButton, request ? '依頼する' : '依頼を取り消す'),
        );
        await tester.pumpAndSettle();
        expect(requested, request);
      }
      expect(writes.length, 2);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets(
    'operator waits for persisted cleanup and stops polling on completion',
    (tester) async {
      var deleted = false, reads = 0;
      final writes = <http.Request>[];
      final client = MockClient((r) async {
        expect(r.url.path, '/api/v1/operations/publishers/club-a/deletion');
        if (r.method == 'DELETE') {
          writes.add(r);
          expect(jsonDecode(r.body), {'confirmation': 'club-a'});
          deleted = true;
          return publisherJson({
            'state': 'deleting',
            'stage': 'cache',
            'prefix': 0,
            'prefix_total': 3,
          });
        }
        reads++;
        return publisherJson(
          deleted
              ? {
                  'state': 'deleted',
                  'stage': 'complete',
                  'prefix': 3,
                  'prefix_total': 3,
                }
              : {'state': 'active', 'requested': true},
        );
      });
      addTearDown(client.close);
      await tester.pumpWidget(
        MaterialApp(
          home: PublisherDeletionScreen(
            api: PublisherApi(endpoint: endpoint, client: client),
            account: account,
            operator: true,
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(OutlinedButton, '削除を開始'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextFormField, '団体ID'),
        'club-a',
      );
      await tester.tap(find.widgetWithText(FilledButton, '削除を開始'));
      await tester.pump(const Duration(milliseconds: 400));
      expect(writes.length, 1);
      expect(find.text('配信キャッシュを削除しています'), findsOneWidget);
      expect(find.text('団体を削除しました'), findsNothing);
      await tester.pump(const Duration(seconds: 2));
      await tester.pumpAndSettle();
      expect(find.text('団体を削除しました'), findsOneWidget);
      final after = reads;
      await tester.pump(const Duration(seconds: 10));
      expect(reads, after);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );

  testWidgets('closing deletion progress cancels client polling', (
    tester,
  ) async {
    var requests = 0;
    final client = MockClient((_) async {
      requests++;
      return publisherJson({
        'state': 'deleting',
        'stage': 'jobs',
        'prefix': 0,
        'prefix_total': 3,
      });
    });
    addTearDown(client.close);
    await tester.pumpWidget(
      MaterialApp(
        home: PublisherDeletionScreen(
          api: PublisherApi(endpoint: endpoint, client: client),
          account: account,
          operator: true,
        ),
      ),
    );
    await tester.pump();
    await tester.pumpWidget(const SizedBox());
    await tester.pump(const Duration(seconds: 10));
    expect(requests, 1);
    expect(tester.takeException(), isNull);
  });
}
