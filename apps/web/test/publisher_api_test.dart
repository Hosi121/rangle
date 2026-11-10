import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/generation/generation_api.dart';
import 'package:rangle_web/publishers/publisher_api.dart';
import 'package:rangle_web/publishers/publisher_controller.dart';

import 'support/publisher_fixture.dart';

void main() {
  final endpoint = Uri.parse('https://app.example.test/api/v1/');
  test(
    'generation scope uses account-local routes without trusting body claims',
    () async {
      final paths = <String>[];
      final client = MockClient((r) async {
        paths.add(r.url.path);
        expect(r.headers.containsKey('authorization'), isFalse);
        if (r.url.path.endsWith('presets')) {
          return publisherJson({'available': true, 'presets': <Object?>[]});
        }
        return publisherJson({'jobs': <Object?>[]});
      });
      final api = GenerationApi(
        endpoint: endpoint,
        publisherAccountId: 'club-a',
        client: client,
      );
      await api.presets();
      await api.jobs();
      expect(paths, [
        '/api/v1/publishers/club-a/generation/presets',
        '/api/v1/publishers/club-a/generation/jobs',
      ]);
      expect(
        () => GenerationApi(endpoint: endpoint, publisherAccountId: '../admin'),
        throwsFormatException,
      );
      api.close();
      client.close();
    },
  );
  test(
    'late session response and late login cannot resurrect old staff authority',
    () async {
      final sessionReply = Completer<http.Response>();
      final client = MockClient(
        (r) async => r.url.path.endsWith('/me')
            ? sessionReply.future
            : publisherJson({'identity': publisherIdentity()}),
      );
      final controller = PublisherController(
        PublisherApi(endpoint: endpoint, client: client),
      );
      final initializing = controller.initialize();
      await controller.login('club-a', 'editor', 'a long test password 2026');
      sessionReply.complete(
        publisherJson({
          'available': true,
          'identity': publisherIdentity(account: 'club-b'),
        }),
      );
      await initializing;
      expect(controller.identity!.account.id, 'club-a');
      controller.dispose();
      client.close();

      final loginReply = Completer<http.Response>();
      final secondClient = MockClient((_) => loginReply.future);
      final second = PublisherController(
        PublisherApi(endpoint: endpoint, client: secondClient),
      );
      final loggingIn = second.login(
        'club-a',
        'editor',
        'a long test password 2026',
      );
      second.expired();
      loginReply.complete(publisherJson({'identity': publisherIdentity()}));
      await loggingIn;
      expect(second.identity, isNull);
      second.dispose();
      secondClient.close();
    },
  );
  test(
    'staff reset and input assignment send only the supported payload',
    () async {
      final requests = <http.Request>[];
      final client = MockClient((r) async {
        requests.add(r);
        if (r.url.path.endsWith('/password')) return http.Response('', 204);
        return publisherJson({
          'presets': [
            {
              'id': 'sample',
              'title': 'Sample',
              'mode': 'dynamic',
              'enabled': true,
            },
          ],
        });
      });
      final api = PublisherApi(endpoint: endpoint, client: client);
      await api.resetPassword(
        'club-a',
        'staff-a',
        'a replacement password 2026',
      );
      await api.assignInputs('club-a', {'sample'});
      expect(
        requests[0].url.path,
        '/api/v1/publishers/club-a/staff/staff-a/password',
      );
      expect(jsonDecode(requests[0].body), {
        'password': 'a replacement password 2026',
      });
      expect(
        requests[1].url.path,
        '/api/v1/operations/publishers/club-a/generation/presets',
      );
      expect(jsonDecode(requests[1].body), {
        'presets': ['sample'],
      });
      expect(
        () => PublisherIdentity.fromJson({
          'account': publisherIdentity()['account'],
          'staff': {
            'id': 'staff-a',
            'username': 'editor',
            'role': 'operator',
            'disabled': false,
          },
        }),
        throwsFormatException,
      );
      api.close();
      client.close();
    },
  );
}
