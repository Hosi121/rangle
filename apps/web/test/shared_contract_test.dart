import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/live/live_api.dart';
import 'package:rangle_web/viewer/playback_state.dart';

void main() {
  final root = Directory('../../testdata/contracts/v1');
  final corpus = jsonDecode(
    File('${root.path}/cases.json').readAsStringSync(),
  ) as Map<String, dynamic>;
  for (final entry in corpus['cases'] as List) {
    final row = entry as Map<String, dynamic>;
    if (row['profile'] == 'live') {
      test('shared contract ${row['id']} (raw live HTTP)', () async {
        Future<void> run() async {
          final recipe = row['recipe'] as Map<String, dynamic>?;
          final body = recipe == null
              ? File('${root.path}/${row['file']}').readAsStringSync()
              : ' ' * (recipe['bytes'] as int);
          final client = MockClient((_) async => http.Response(body, 200));
          try {
            final api = LiveApi(
              endpoint: Uri.parse('https://app.example.test/api/v1/live'),
              appUri: Uri.parse('https://app.example.test/'),
              mediaOrigin:
                  row['origin'] as String? ??
                  corpus['context']['origin'] as String,
              client: client,
            );
            await api.poll(abort: Completer<void>().future);
          } finally {
            client.close();
          }
        }

        if (row['accept'] == true) {
          await run();
        } else {
          await expectLater(
            run(),
            throwsA(anyOf(isA<FormatException>(), isA<http.ClientException>())),
          );
        }
      });
    } else if (row['profile'] == 'clock') {
      test('shared contract ${row['id']} (sample timeline)', () {
        final value = jsonDecode(
          File('${root.path}/${row['file']}').readAsStringSync(),
        ) as Map<String, dynamic>;
        final timeline = ViewerTimeline(
          frameCount: value['frameCount'] as int,
          frameRate: (value['frameRate'] as num).toDouble(),
        );
        expect(
          timeline.atFrame(timeline.frameCount - 1).inMicroseconds / 1000000,
          closeTo(value['end'] as num, 0.000001),
        );
        final queries = value['queries'] as List,
            frames = value['frames'] as List;
        for (var i = 0; i < queries.length; i++) {
          expect(
            timeline.frameAt(
              Duration(microseconds: ((queries[i] as num) * 1000000).round()),
            ),
            frames[i],
          );
        }
      });
    }
  }
}
