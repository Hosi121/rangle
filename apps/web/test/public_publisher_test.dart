import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/community/account_controller.dart';
import 'package:rangle_web/community/community_api.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/publishers/public_publisher_api.dart';
import 'package:rangle_web/publishers/public_publisher_screen.dart';
import 'package:rangle_web/publishers/subscriptions_dialog.dart';

import 'support/fake_viewer.dart';
import 'support/publisher_fixture.dart';

const profile = {'id': 'club-a', 'name': 'デモ団体', 'kind': 'organization'};
Map<String, Object> video(String id) => {
  'id': id,
  'revision': 'v1',
  'title': '公開動画 $id',
  'sport': 'バスケットボール',
  'description': '',
  'context': {'publisher': profile},
};
AccountController viewer(http.Client client) => AccountController(
  CommunityApi(
    endpoint: Uri.parse('https://app.example.test/api/v1/'),
    client: client,
  ),
)..user = const AccountUser('viewer', 'Viewer');

void main() {
  testWidgets(
    'publisher page loads only matching paged videos and toggles a personal follow at mobile width',
    (tester) async {
      tester.view.physicalSize = const Size(320, 740);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final requests = <http.Request>[];
      var subscribed = false;
      final client = MockClient((r) async {
        requests.add(r);
        switch (r.url.path) {
          case '/api/v1/publishers/club-a':
            return publisherJson({'publisher': profile});
          case '/api/v1/publishers/club-a/subscription':
            if (r.method == 'PUT') subscribed = true;
            if (r.method == 'DELETE') subscribed = false;
            return publisherJson({'subscribed': subscribed});
          case '/api/v1/clips':
            expect(r.url.queryParameters['publisher'], 'club-a');
            return publisherJson({
              'clips': [
                video(
                  r.url.queryParameters['cursor'] == null ? 'first' : 'second',
                ),
              ],
              if (!r.url.queryParameters.containsKey('cursor'))
                'next_cursor': 'next',
            });
          default:
            return publisherJson({'error': 'not_found'}, 404);
        }
      });
      final account = viewer(client);
      addTearDown(account.dispose);
      addTearDown(client.close);
      await tester.pumpWidget(
        MaterialApp(
          home: PublicPublisherScreen(id: 'club-a', account: account),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('デモ団体'), findsOneWidget);
      expect(find.text('公開動画 first'), findsOneWidget);
      await tester.tap(find.byKey(const Key('publisher-follow')));
      await tester.pumpAndSettle();
      expect(find.text('フォロー中'), findsOneWidget);
      await tester.tap(find.byKey(const Key('publisher-follow')));
      await tester.pumpAndSettle();
      expect(find.text('フォローする'), findsOneWidget);
      await tester.tap(find.text('もっと見る'));
      await tester.pumpAndSettle();
      expect(find.text('公開動画 second'), findsOneWidget);
      expect(requests.where((r) => r.method != 'GET').map((r) => r.method), [
        'PUT',
        'DELETE',
      ]);
      expect(
        requests.where(
          (r) =>
              r.url.path.contains('publisher-auth') ||
              r.url.path.contains('generation'),
        ),
        isEmpty,
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'publisher page does not restore a former viewer follow after logout',
    (tester) async {
      final pending = Completer<http.Response>();
      final client = MockClient((r) async {
        if (r.url.path.endsWith('/subscription')) return pending.future;
        if (r.url.path.endsWith('/clips')) {
          return publisherJson({'clips': <Object>[]});
        }
        return publisherJson({'publisher': profile});
      });
      final account = viewer(client);
      addTearDown(account.dispose);
      addTearDown(client.close);
      await tester.pumpWidget(
        MaterialApp(
          home: PublicPublisherScreen(id: 'club-a', account: account),
        ),
      );
      await tester.pump();
      await tester.pump();
      account.expired();
      pending.complete(publisherJson({'subscribed': true}));
      await tester.pumpAndSettle();
      expect(find.text('フォロー中'), findsNothing);
      expect(find.text('ログインしてフォロー'), findsOneWidget);
    },
  );

  testWidgets(
    'subscriptions retry and paginate, and clear private rows when the viewer changes',
    (tester) async {
      var reads = 0;
      final client = MockClient((r) async {
        reads++;
        if (reads == 1) {
          return publisherJson({'error': 'service_unavailable'}, 503);
        }
        final first = r.url.queryParameters['cursor'] == null;
        return publisherJson({
          'publishers': [
            first ? profile : {...profile, 'id': 'club-b', 'name': '別の団体'},
          ],
          'next_cursor': first ? 'next' : '',
        });
      });
      final account = viewer(client);
      addTearDown(account.dispose);
      addTearDown(client.close);
      await tester.pumpWidget(
        MaterialApp(home: SubscriptionsDialog(account: account)),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('再読み込み'));
      await tester.pumpAndSettle();
      expect(find.text('デモ団体'), findsOneWidget);
      await tester.tap(find.text('もっと見る'));
      await tester.pumpAndSettle();
      expect(find.text('別の団体'), findsOneWidget);
      account.expired();
      await tester.pumpAndSettle();
      expect(find.text('デモ団体'), findsNothing);
      expect(find.text('別の団体'), findsNothing);
    },
  );

  testWidgets(
    'public organization deep link opens anonymously and a listed clip returns to playback',
    (tester) async {
      final client = MockClient(
        (r) async => switch (r.url.path) {
          '/api/v1/auth/me' => publisherJson({'available': true, 'user': null}),
          '/api/v1/publishers/club-a' => publisherJson({'publisher': profile}),
          '/api/v1/clips' => publisherJson({
            'clips': [video('demo-01')],
          }),
          _ => publisherJson({'error': 'not_found'}, 404),
        },
      );
      addTearDown(client.close);
      await tester.pumpWidget(
        RangleApp(
          repository: const DemoClipRepository(),
          community: CommunityApi(
            endpoint: Uri.parse('https://app.example.test/api/v1/'),
            client: client,
          ),
          initialPublicPublisherId: 'club-a',
          viewerFactory: (_) => FakeViewer(),
          sceneFactory: (_) => FakeViewer(),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('デモ団体'), findsOneWidget);
      expect(find.text('ログインしてフォロー'), findsOneWidget);
      await tester.tap(find.text('公開動画 demo-01'));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('highlight-feed')), findsOneWidget);
      expect(find.text('デモ団体'), findsNothing);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
    },
  );

  test(
    'public subscription DTO rejects identity mismatch and malformed boolean',
    () async {
      final client = MockClient(
        (r) async => r.url.path.endsWith('/subscription')
            ? publisherJson({'subscribed': 'true'})
            : publisherJson({
                'publisher': {...profile, 'id': 'club-b'},
              }),
      );
      addTearDown(client.close);
      final api = CommunityApi(
        endpoint: Uri.parse('https://app.example.test/api/v1/'),
        client: client,
      );
      await expectLater(api.publicPublisher('club-a'), throwsFormatException);
      await expectLater(
        api.publisherSubscription('club-a'),
        throwsFormatException,
      );
    },
  );

  testWidgets('account menu opens a followed publisher page', (tester) async {
    final client = MockClient(
      (r) async => switch (r.url.path) {
        '/api/v1/auth/me' => publisherJson({
          'available': true,
          'user': {'id': 'viewer', 'name': 'Viewer'},
        }),
        '/api/v1/me/subscriptions' => publisherJson({
          'publishers': [profile],
          'next_cursor': '',
        }),
        '/api/v1/publishers/club-a' => publisherJson({'publisher': profile}),
        '/api/v1/publishers/club-a/subscription' => publisherJson({
          'subscribed': true,
        }),
        '/api/v1/clips' => publisherJson({
          'clips': [video('demo-01')],
        }),
        _ => publisherJson({'error': 'not_found'}, 404),
      },
    );
    addTearDown(client.close);
    await tester.pumpWidget(
      RangleApp(
        repository: const DemoClipRepository(),
        community: CommunityApi(
          endpoint: Uri.parse('https://app.example.test/api/v1/'),
          client: client,
        ),
        viewerFactory: (_) => FakeViewer(),
        sceneFactory: (_) => FakeViewer(),
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('アカウント'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('フォロー中の団体'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('デモ団体'));
    await tester.pumpAndSettle();
    expect(find.text('公開動画 demo-01'), findsOneWidget);
    expect(find.text('フォロー中'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.pumpWidget(const SizedBox());
  });

  test('publisher video parser refuses another organization in a filtered response', () async {
    final client = MockClient(
      (_) async => publisherJson({
        'clips': [
          {
            ...video('wrong'),
            'context': {
              'publisher': {...profile, 'id': 'club-b'},
            },
          },
        ],
      }),
    );
    addTearDown(client.close);
    final api = CommunityApi(
      endpoint: Uri.parse('https://app.example.test/api/v1/'),
      client: client,
    );
    await expectLater(api.publisherClips('club-a'), throwsFormatException);
  });
}
