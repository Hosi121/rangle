import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/community/community_api.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/generation/generation_screen.dart';
import 'package:rangle_web/live/live_screen.dart';
import 'package:rangle_web/main.dart';

import 'support/fake_viewer.dart';
import 'support/generation_fixture.dart';

void main() {
  test(
    'management display hint comes from the response with a strict boolean',
    () {
      expect(const AccountUser('admin', 'User').isAdmin, isFalse);
      for (final value in [null, false, 'true', 1]) {
        expect(
          AccountUser.fromJson({
            'id': 'admin',
            'name': 'User',
            'is_admin': value,
          }).isAdmin,
          isFalse,
        );
      }
      expect(
        AccountUser.fromJson({'id': 'admin', 'name': 'User', 'is_admin': true})
            .isAdmin,
        isTrue,
      );
    },
  );
  for (final entry in ['home', 'generation', 'live']) {
    testWidgets('administrator session opens management for $entry', (
      tester,
    ) async {
      final fixture = GenerationFixture();
      addTearDown(fixture.client.close);
      await tester.pumpWidget(
        RangleApp(
          repository: const DemoClipRepository(),
          community: fixture.community,
          generation: fixture.api,
          initialGeneration: entry == 'generation',
          initialLiveId: entry == 'live' ? 'game' : null,
          viewerFactory: (_) => FakeViewer(),
          sceneFactory: (_) => FakeViewer(),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('管理'), findsOneWidget);
      expect(find.byType(GenerationScreen), findsOneWidget);
      expect(find.byType(LiveScreen), findsNothing);
      expect(find.byKey(const Key('highlight-feed')), findsNothing);
      expect(find.text('生成を開始'), findsOneWidget);
      expect(fixture.creates, isEmpty);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }
  for (final anonymous in [true, false]) {
    testWidgets(
      '${anonymous ? 'anonymous' : 'non-admin named admin'} cannot open generation',
      (tester) async {
        final fixture = GenerationFixture(admin: false);
        fixture.user = anonymous
            ? null
            : {'id': 'admin', 'name': 'User', 'is_admin': false};
        addTearDown(fixture.client.close);
        await tester.pumpWidget(
          RangleApp(
            repository: const DemoClipRepository(),
            community: fixture.community,
            generation: fixture.api,
            initialGeneration: true,
            viewerFactory: (_) => FakeViewer(),
            sceneFactory: (_) => FakeViewer(),
          ),
        );
        await tester.pumpAndSettle();
        expect(
          find.text(anonymous ? '管理者ログイン' : '管理者専用のページです'),
          findsOneWidget,
        );
        expect(find.text('生成を開始'), findsNothing);
        expect(
          fixture.requests.where((r) => r.url.path.contains('/generation/')),
          isEmpty,
        );
        expect(fixture.creates, isEmpty);
        await tester.pumpWidget(const SizedBox.shrink());
      },
    );
  }
}
