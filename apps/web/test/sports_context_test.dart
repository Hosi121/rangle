import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:rangle_web/data/api_clip_repository.dart';
import 'package:rangle_web/models/sports_context.dart';
import 'package:rangle_web/widgets/sports_context_details.dart';

Map<String, dynamic> fixture() =>
    jsonDecode(File('../../testdata/sports/context.json').readAsStringSync())
        as Map<String, dynamic>;
void main() {
  test('shared publication fixture is parsed by the catalog client', () async {
    final client = MockClient(
      (_) async => http.Response.bytes(
        utf8.encode(
          jsonEncode({
            'clips': [
              {
                'id': 'sample',
                'title': 'Sample',
                'sport': 'basketball',
                'description': '',
                'context': fixture(),
              },
            ],
          }),
        ),
        200,
      ),
    );
    addTearDown(client.close);
    final clips = await ApiClipRepository(
      endpoint: 'https://example.test/api/v1/clips',
      client: client,
    ).load();
    final context = clips.single.context!;
    expect(context.publisher!.id, 'example-sports');
    expect(context.event!.venue!.name, '例示体育館');
    expect(context.athletes.single.id, 'sample-player');
    expect(context.event!.startsAt!.toUtc(), DateTime.utc(2026, 11, 14, 6));
  });
  test('unknown publisher types and duplicate athletes are rejected', () {
    final personal = fixture();
    (personal['publisher'] as Map<String, dynamic>)['kind'] = 'person';
    expect(() => SportsContext.fromJson(personal), throwsFormatException);
    final duplicate = fixture();
    (duplicate['athletes'] as List).add((duplicate['athletes'] as List).first);
    expect(() => SportsContext.fromJson(duplicate), throwsFormatException);
    expect(() => SportsContext.fromJson({}), throwsFormatException);
  });
  testWidgets('overview names initiate contextual search at mobile width', (
    tester,
  ) async {
    String? query;
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: SizedBox(
              width: 320,
              child: SportsContextDetails(
                context: SportsContext.fromJson(fixture()),
                onSearch: (name) => query = name,
              ),
            ),
          ),
        ),
      ),
    );
    expect(find.text('投稿者'), findsOneWidget);
    expect(find.text('会場・東京'), findsOneWidget);
    await tester.ensureVisible(find.text('サンプル選手'));
    await tester.tap(find.text('サンプル選手'));
    expect(query, 'サンプル選手');
    expect(tester.takeException(), isNull);
  });
}
