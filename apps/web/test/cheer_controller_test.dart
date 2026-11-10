import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/community/cheer_controller.dart';
import 'package:rangle_web/community/community_api.dart';

void main() {
  test('lost response retries the same receipt while new taps remain a separate batch', () async {
    final bins = List.filled(48, 0),
        receipts = <String>{},
        requests = <Map<String, dynamic>>[];
    final stale = Completer<http.Response>();
    final client = MockClient((request) async {
      if (request.method == 'GET') return stale.future;
      final body = jsonDecode(request.body) as Map<String, dynamic>;
      requests.add(body);
      if (receipts.add(body['request_id'] as String)) {
        final added = (body['bins'] as List).cast<int>();
        for (var i = 0; i < 48; i++) {
          bins[i] += added[i];
        }
      }
      return http.Response(
        jsonEncode(
          requests.length == 1
              ? {'error': 'service_unavailable'}
              : {'bins': bins},
        ),
        requests.length == 1 ? 503 : 200,
      );
    });
    final controller = CheerController(
      CommunityApi(
        endpoint: Uri.parse('https://rangle.test/api/v1/'),
        client: client,
      ),
      'clip',
    );
    final loading = controller.load();
    controller.cheer(const Duration(seconds: 5), const Duration(seconds: 20));
    await controller.flush();
    controller.cheer(const Duration(seconds: 15), const Duration(seconds: 20));
    await controller.flush();
    expect(requests[0]['request_id'], requests[1]['request_id']);
    expect(requests[0]['bins'], requests[1]['bins']);
    await controller.flush();
    expect(requests[2]['request_id'], isNot(requests[0]['request_id']));
    stale.complete(
      http.Response(jsonEncode({'bins': List.filled(48, 0)}), 200),
    );
    await loading;
    expect(controller.total, 2);
    expect(controller.bins[12], 1);
    expect(controller.bins[36], 1);
    expect(controller.hasPending, isFalse);
    controller.dispose();
    client.close();
  });

  test(
    'card eviction drains bounded batches and preserves the last frame bin',
    () async {
      final bins = List.filled(48, 0);
      final sizes = <int>[];
      final client = MockClient((request) async {
        final body = jsonDecode(request.body) as Map<String, dynamic>;
        final added = (body['bins'] as List).cast<int>();
        sizes.add(added.reduce((a, b) => a + b));
        for (var i = 0; i < 48; i++) {
          bins[i] += added[i];
        }
        return http.Response(jsonEncode({'bins': bins}), 200);
      });
      final controller = CheerController(
        CommunityApi(
          endpoint: Uri.parse('https://rangle.test/api/v1/'),
          client: client,
        ),
        'clip',
      );
      for (var i = 0; i < 90; i++) {
        controller.cheer(
          const Duration(seconds: 20),
          const Duration(seconds: 20),
        );
      }
      await controller.finish();
      expect(sizes, [64, 26]);
      expect(bins.last, 90);
      client.close();
    },
  );
}
