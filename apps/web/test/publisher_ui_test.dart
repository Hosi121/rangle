import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/community/account_controller.dart';
import 'package:rangle_web/community/community_api.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/generation/generation_screen.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/generation/generation_api.dart';
import 'package:rangle_web/models/publisher_account.dart';
import 'package:rangle_web/publishers/operations_publishers_screen.dart';
import 'package:rangle_web/publishers/publisher_api.dart';
import 'package:rangle_web/publishers/publisher_home.dart';
import 'package:rangle_web/publishers/publisher_staff_screen.dart';

import 'support/publisher_fixture.dart';
import 'support/generation_fixture.dart';
import 'support/fake_viewer.dart';

void mobileSize(WidgetTester tester) {
  tester.view.physicalSize = const Size(320, 740);
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
}

void main() {
  final endpoint = Uri.parse('https://app.example.test/api/v1/');
  testWidgets(
    'staff login uses the separate session and editor menu at mobile width',
    (tester) async {
      mobileSize(tester);
      final paths = <String>[];
      final client = MockClient((r) async {
        paths.add(r.url.path);
        return switch (r.url.path) {
          '/api/v1/publisher-auth/me' => publisherJson({
            'available': true,
            'identity': null,
          }),
          '/api/v1/publisher-auth/login' => publisherJson({
            'identity': publisherIdentity(),
          }),
          '/api/v1/publishers/club-a/generation/presets' => publisherJson({
            'available': true,
            'presets': <Object?>[],
          }),
          '/api/v1/publishers/club-a/generation/jobs' => publisherJson({
            'jobs': <Object?>[],
          }),
          '/api/v1/publisher-auth/logout' => http.Response('', 204),
          _ => publisherJson({'error': 'not_found'}, 404),
        };
      });
      await tester.pumpWidget(
        MaterialApp(
          home: PublisherHome(
            api: PublisherApi(endpoint: endpoint, client: client),
            generation: GenerationApi(
              endpoint: endpoint,
              publisherAccountId: 'club-a',
              client: client,
            ),
            onOpenResult: (_) {},
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const Key('publisher-login-account')),
        'club-a',
      );
      await tester.enterText(
        find.byKey(const Key('publisher-login-username')),
        'editor',
      );
      await tester.enterText(
        find.byKey(const Key('publisher-login-password')),
        'a long staff password 2026',
      );
      await tester.ensureVisible(find.widgetWithText(FilledButton, 'ログイン'));
      await tester.tap(find.widgetWithText(FilledButton, 'ログイン'));
      await tester.pumpAndSettle();
      expect(find.text('団体の生成'), findsOneWidget);
      expect(find.text('利用できるデータがありません。'), findsOneWidget);
      await tester.tap(find.byTooltip('団体アカウント'));
      await tester.pumpAndSettle();
      expect(find.text('担当者管理'), findsNothing);
      expect(find.text('パスワード変更'), findsOneWidget);
      await tester.tap(find.text('ログアウト'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('publisher-login-account')), findsOneWidget);
      expect(
        paths.where(
          (p) =>
              p.startsWith('/api/v1/generation/') ||
              p.startsWith('/api/v1/auth/'),
        ),
        isEmpty,
      );
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      client.close();
    },
  );

  testWidgets(
    'administrator resets staff password without changing role or actor scope',
    (tester) async {
      mobileSize(tester);
      final requests = <http.Request>[];
      final client = MockClient((r) async {
        requests.add(r);
        if (r.method == 'PUT') return http.Response('', 204);
        return publisherJson({
          'staff': [publisherIdentity()['staff']],
          'next_cursor': '',
        });
      });
      final api = PublisherApi(endpoint: endpoint, client: client);
      await tester.pumpWidget(
        MaterialApp(
          home: PublisherStaffScreen(
            api: api,
            account: const PublisherAccount(
              id: 'club-a',
              name: 'デモ団体',
              kind: 'organization',
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('editorを管理'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('パスワード再設定'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.byKey(const Key('publisher-new-password')),
        'a replacement password 2026',
      );
      await tester.enterText(
        find.byKey(const Key('publisher-confirm-password')),
        'a replacement password 2026',
      );
      await tester.ensureVisible(find.widgetWithText(FilledButton, '保存'));
      await tester.tap(find.widgetWithText(FilledButton, '保存'));
      await tester.pumpAndSettle();
      final reset = requests.singleWhere((r) => r.method == 'PUT');
      expect(
        reset.url.path,
        '/api/v1/publishers/club-a/staff/staff-a/password',
      );
      expect(jsonDecode(reset.body), {
        'password': 'a replacement password 2026',
      });
      expect(find.text('投稿担当'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      api.close();
      client.close();
    },
  );

  testWidgets(
    'operator provisions organization and assigns an existing reviewed input',
    (tester) async {
      mobileSize(tester);
      final requests = <http.Request>[];
      bool created = false, enabled = false;
      final client = MockClient((r) async {
        requests.add(r);
        if (r.url.path == '/api/v1/auth/me') {
          return publisherJson({
            'available': true,
            'user': {'id': 'admin', 'name': 'Operator', 'is_admin': true},
          });
        }
        if (r.url.path == '/api/v1/operations/publishers') {
          if (r.method == 'POST') {
            created = true;
            return publisherJson({
              'account': publisherIdentity()['account'],
            }, 201);
          }
          return publisherJson({
            'publishers': created
                ? [
                    {
                      'account': publisherIdentity()['account'],
                      'status': 'active',
                    },
                  ]
                : <Object?>[],
            'next_cursor': '',
          });
        }
        if (r.url.path.endsWith('/generation/presets')) {
          if (r.method == 'PUT') enabled = true;
          return publisherJson({
            'presets': [
              {
                'id': 'reviewed',
                'title': '確認済みの映像',
                'mode': 'dynamic',
                'enabled': enabled,
              },
            ],
          });
        }
        return publisherJson({'error': 'not_found'}, 404);
      });
      final account = AccountController(
        CommunityApi(endpoint: endpoint, client: client),
      );
      await account.initialize();
      final api = PublisherApi(endpoint: endpoint, client: client);
      await tester.pumpWidget(
        MaterialApp(
          home: OperationsPublishersScreen(account: account, api: api),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(FilledButton, '団体を登録'));
      await tester.pumpAndSettle();
      for (final field in {
        'publisher-create-id': 'club-a',
        'publisher-create-name': 'デモ団体',
        'publisher-create-username': 'manager',
        'publisher-new-password': 'a long staff password 2026',
        'publisher-confirm-password': 'a long staff password 2026',
      }.entries) {
        final finder = find.byKey(Key(field.key));
        await tester.ensureVisible(finder);
        await tester.enterText(finder, field.value);
      }
      await tester.ensureVisible(find.widgetWithText(FilledButton, '登録'));
      await tester.tap(find.widgetWithText(FilledButton, '登録'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('デモ団体'));
      await tester.pumpAndSettle();
      await tester.tap(find.byType(CheckboxListTile));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.widgetWithText(FilledButton, '割当を保存'));
      await tester.tap(find.widgetWithText(FilledButton, '割当を保存'));
      await tester.pumpAndSettle();
      final provision = requests.singleWhere((r) => r.method == 'POST');
      expect((jsonDecode(provision.body) as Map)['staff_username'], 'manager');
      final grant = requests.singleWhere((r) => r.method == 'PUT');
      expect(
        grant.url.path,
        '/api/v1/operations/publishers/club-a/generation/presets',
      );
      expect(jsonDecode(grant.body), {
        'presets': ['reviewed'],
      });
      expect(find.text('入力の割当を保存しました。'), findsOneWidget);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      account.dispose();
      api.close();
      client.close();
    },
  );
  testWidgets(
    'publisher deep link never substitutes an operator session and returns to management',
    (tester) async {
      mobileSize(tester);
      final fixture = GenerationFixture();
      final client = MockClient(
        (r) async => publisherJson({'available': true, 'identity': null}),
      );
      await tester.pumpWidget(
        RangleApp(
          repository: const DemoClipRepository(),
          community: fixture.community,
          generation: fixture.api,
          publisher: PublisherApi(endpoint: endpoint, client: client),
          initialPublisher: true,
          viewerFactory: (_) => FakeViewer(),
          sceneFactory: (_) => FakeViewer(),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('publisher-login-account')), findsOneWidget);
      expect(find.byType(GenerationScreen), findsNothing);
      await tester.tap(find.byType(BackButton));
      await tester.pumpAndSettle();
      expect(find.byType(GenerationScreen), findsOneWidget);
      expect(find.byKey(const Key('publisher-login-account')), findsNothing);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      fixture.client.close();
      client.close();
    },
  );
}
