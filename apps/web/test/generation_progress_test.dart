import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';
import 'package:rangle_web/generation/generation_api.dart';
import 'package:rangle_web/generation/generation_progress.dart';
import 'package:rangle_web/publishers/publisher_api.dart';
import 'package:rangle_web/publishers/publisher_home.dart';

import 'support/generation_fixture.dart';
import 'support/publisher_fixture.dart';

Map<String, dynamic> measured({
  String stage = 'training',
  bool stale = false,
  int? eta = 120,
}) => {
  'stage': stage,
  if (stage == 'training') ...{'completed_steps': 500, 'total_steps': 1000},
  'observed_at': '2026-11-10T00:00:00Z',
  'stale': stale,
  'eta_seconds': ?eta,
};

void reducedMotion() {
  TestWidgetsFlutterBinding.ensureInitialized()
      .platformDispatcher
      .accessibilityFeaturesTestValue = const FakeAccessibilityFeatures(
    disableAnimations: true,
  );
}

void main() {
  setUp(reducedMotion);
  tearDown(
    () => TestWidgetsFlutterBinding.ensureInitialized().platformDispatcher
        .clearAccessibilityFeaturesTestValue(),
  );

  test('progress accepts optional old responses and rejects invalid counters or ETA', () {
    expect(GenerationJob.fromJson(generationJob('running')).progress, isNull);
    final p = GenerationProgress.fromJson(measured());
    expect(p.fraction, .5);
    expect(p.etaLabel, '学習の残り目安：約2分');
    for (final data in [
      {...measured(), 'completed_steps': -1},
      {...measured(), 'total_steps': 0},
      {...measured(), 'completed_steps': 1001},
      {...measured(), 'eta_seconds': -1},
      {...measured(), 'eta_seconds': 1801},
      measured(stale: true),
      measured(stage: 'finalizing'),
    ]) {
      expect(() => GenerationProgress.fromJson(data), throwsFormatException);
    }
  });

  testWidgets(
    'measured training shows a determinate bar and ETA at mobile width',
    (tester) async {
      tester.view.physicalSize = const Size(320, 740);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);
      final job = GenerationJob.fromJson({
        ...generationJob('running'),
        'progress': measured(),
      });
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(body: GenerationProgressIndicator(job: job)),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('学習 50%'), findsOneWidget);
      expect(find.text('学習の残り目安：約2分'), findsOneWidget);
      expect(
        tester
            .widget<LinearProgressIndicator>(
              find.byType(LinearProgressIndicator),
            )
            .value,
        .5,
      );
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'queued, stale, finalizing and terminal states do not invent an ETA',
    (tester) async {
      for (final data in [
        generationJob('queued'),
        {
          ...generationJob('running'),
          'progress': measured(stale: true, eta: null),
        },
        {
          ...generationJob('running'),
          'progress': measured(stage: 'finalizing', eta: null),
        },
        generationJob('succeeded'),
        generationJob('failed'),
        generationJob('cancelled'),
      ]) {
        await tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: GenerationProgressIndicator(
                job: GenerationJob.fromJson(data),
              ),
            ),
          ),
        );
        await tester.pumpAndSettle();
        expect(find.textContaining('学習の残り目安'), findsNothing);
        if (data['state'] == 'succeeded') {
          expect(
            tester
                .widget<LinearProgressIndicator>(
                  find.byType(LinearProgressIndicator),
                )
                .value,
            1,
          );
        } else if (data['state'] == 'queued') {
          expect(
            tester
                .widget<LinearProgressIndicator>(
                  find.byType(LinearProgressIndicator),
                )
                .value,
            isNull,
          );
          expect(find.text('生成の準備待ち'), findsOneWidget);
        } else if (data['state'] == 'failed' || data['state'] == 'cancelled') {
          expect(find.byType(LinearProgressIndicator), findsNothing);
        }
      }
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'publisher polling updates progress then completion within its own scope',
    (tester) async {
      var finished = false;
      final paths = <String>[];
      final client = MockClient((request) async {
        paths.add(request.url.path);
        if (request.url.path.endsWith('/publisher-auth/me')) {
          return publisherJson({
            'available': true,
            'identity': publisherIdentity(),
          });
        }
        if (request.url.path.endsWith('/presets')) {
          return publisherJson({'available': true, 'presets': <Object?>[]});
        }
        if (request.url.path.endsWith('/jobs')) {
          return publisherJson({
            'jobs': [generationJob('running', mode: 'dynamic')],
          });
        }
        return publisherJson({
          ...generationResult(
            finished ? 'succeeded' : 'running',
            mode: 'dynamic',
          ),
          'job': {
            ...generationJob(
              finished ? 'succeeded' : 'running',
              mode: 'dynamic',
            ),
            if (!finished) 'progress': measured(),
          },
        });
      });
      addTearDown(client.close);
      final api = PublisherApi(
        endpoint: Uri.parse('https://app.test/api/v1/'),
        client: client,
      );
      final generation = GenerationApi(
        endpoint: Uri.parse('https://app.test/api/v1/'),
        publisherAccountId: 'club-a',
        client: client,
      );
      await tester.pumpWidget(
        MaterialApp(
          home: PublisherHome(
            api: api,
            generation: generation,
            onOpenResult: (_) {},
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(find.text('学習 50%'), findsOneWidget);
      expect(find.text('学習の残り目安：約2分'), findsOneWidget);
      finished = true;
      await tester.pump(const Duration(seconds: 4));
      await tester.pumpAndSettle();
      expect(find.text('生成が完了しました'), findsOneWidget);
      expect(find.textContaining('学習の残り目安'), findsNothing);
      expect(paths.where((p) => p.startsWith('/api/v1/generation/')), isEmpty);
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      generation.close();
    },
  );
}
