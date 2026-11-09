import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';
import 'package:rangle_web/config/app_config.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/live/live_api.dart';
import 'package:rangle_web/live/live_controller.dart';
import 'package:rangle_web/live/live_screen.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/rangle_header.dart';

import 'support/fake_viewer.dart';

const base = 'https://app.example.test';
final digest = List.filled(64, 'a').join();
Map<String, Object?> liveSession({
  int sequence = 1,
  String state = 'live',
  String? origin,
}) {
  return {
    'id': 'game',
    'title': 'バスケットボール LIVE',
    'state': state,
    'started_at': '2026-11-09T00:00:00Z',
    'updated_at': '2026-11-09T00:00:01Z',
    'latest_input_time_seconds': sequence * .8,
    'target_latency_seconds': 30,
    'dropped_windows': 0,
    'source': {
      'label': 'Example cameras',
      'url': 'https://source.example.test',
      'license': 'Test license',
      'caveat': 'Synthetic test source.',
    },
    'segments': [
      if (sequence > 0)
        {
          'sequence': sequence,
          'start_seconds': (sequence - 1) * .8,
          'end_seconds': (sequence - 1) * .8 + .76,
          'scene_url':
              '${origin ?? ''}/live-assets/game/${sequence.toString().padLeft(6, '0')}-${digest.substring(0, 16)}/scene.json',
          'model_sha256': digest,
          'model_bytes': 1000,
          'captured_at': '2026-11-09T00:00:00Z',
          'available_at': '2026-11-09T00:00:01Z',
          'training_seconds': 0.1,
          'mean_packed_psnr': 20,
          'mean_packed_ssim': 0.8,
        },
    ],
  };
}

http.Response reply(Map<String, Object?> body, {String etag = '"one"'}) =>
    http.Response(
      jsonEncode(body),
      200,
      headers: {'etag': etag, 'content-type': 'application/json'},
    );
LiveApi api(http.Client client, {String origin = ''}) => LiveApi(
  endpoint: Uri.parse('$base/api/v1/live'),
  appUri: Uri.parse('$base/demo/'),
  mediaOrigin: origin,
  client: client,
);

class UpdatingViewer extends ViewerAdapter {
  UpdatingViewer(Uri source, {ViewerStatus status = ViewerStatus.ready})
    : _value = ViewerValue(
        status: status,
        source: source,
        duration: const Duration(milliseconds: 760),
        playing: true,
      );
  ViewerValue _value;
  final updates = <Uri>[];
  final pending = <Completer<bool>>[];
  bool disposed = false;
  int pauses = 0;
  @override
  ViewerValue get value => _value;
  @override
  Future<void> initialize() async {}
  @override
  Future<void> seek(Duration position) async {}
  @override
  Future<void> toggleMute() async {}
  @override
  Future<void> pause() async => pauses++;
  @override
  Future<void> resume() async {}
  @override
  Future<void> togglePlayback() async {
    _value = ViewerValue(
      status: ViewerStatus.ready,
      source: value.source,
      duration: value.duration,
      playing: !value.playing,
    );
    notifyListeners();
  }

  @override
  Future<bool> updateSource(Uri source, {String? sha256}) {
    updates.add(source);
    final completion = Completer<bool>();
    pending.add(completion);
    return completion.future;
  }

  void finish(bool ready, {int? at}) {
    final index = at ?? pending.length - 1;
    if (ready) {
      _value = ViewerValue(
        status: ViewerStatus.ready,
        source: updates[index],
        duration: value.duration,
        playing: value.playing,
      );
      notifyListeners();
    }
    pending[index].complete(ready);
  }

  @override
  Widget buildView() =>
      const ColoredBox(key: Key('live-surface'), color: Colors.black);
  @override
  void dispose() {
    disposed = true;
    for (final completion in pending) {
      if (!completion.isCompleted) completion.complete(false);
    }
    super.dispose();
  }
}

void main() {
  test('live API uses ETag/304 and bounds snapshots without trusting returned media origins', () async {
    var calls = 0;
    final service = api(
      MockClient((request) async {
        calls++;
        expect(request.followRedirects, isFalse);
        expect(request.url.path, '/api/v1/live/game');
        if (calls == 1) return reply(liveSession());
        expect(request.headers['If-None-Match'], '"one"');
        return http.Response('', 304);
      }),
    );
    final first = await service.poll(
      id: 'game',
      abort: Completer<void>().future,
    );
    expect(first.sessions!.single.latest!.sequence, 1);
    final unchanged = await service.poll(
      id: 'game',
      etag: first.etag,
      abort: Completer<void>().future,
    );
    expect(unchanged.sessions, isNull);
    expect(
      () => LiveSession.fromJson(
        liveSession(origin: 'https://evil.example.test'),
        service,
      ),
      throwsFormatException,
    );
    final oversized = liveSession();
    oversized['segments'] = List.filled(
      33,
      (liveSession()['segments'] as List).single,
    );
    expect(
      () => LiveSession.fromJson(oversized, service),
      throwsFormatException,
    );
    final malformed = liveSession();
    (malformed['segments'] as List).single['scene_url'] =
        '/live-assets/other/000001-${digest.substring(0, 16)}/scene.json';
    expect(
      () => LiveSession.fromJson(malformed, service),
      throwsFormatException,
    );
  });

  test(
    'configured media CDN handles only detail pointers; list stays on API',
    () async {
      final paths = <Uri>[];
      final service = api(
        MockClient((request) async {
          paths.add(request.url);
          expect(request.headers.containsKey('If-None-Match'), isFalse);
          return request.url.path == '/api/v1/live'
              ? reply({
                  'version': 1,
                  'sessions': [
                    liveSession(origin: 'https://media.example.test'),
                  ],
                })
              : reply(liveSession(origin: 'https://media.example.test'));
        }),
        origin: 'https://media.example.test',
      );
      await service.poll(abort: Completer<void>().future);
      await service.poll(
        id: 'game',
        etag: '"old"',
        abort: Completer<void>().future,
      );
      expect(paths.map((u) => u.toString()), [
        '$base/api/v1/live',
        'https://media.example.test/live-index/game.json',
      ]);
      final config = AppConfig(
        apiUrl: 'https://api.example.test/api/v1/clips?tag=basketball',
        liveMediaOrigin: 'https://media.example.test',
      );
      final configured = config.createLiveApi(
        baseUri: Uri.parse('$base/demo/'),
      )!;
      expect(
        configured.endpoint.toString(),
        'https://api.example.test/api/v1/live',
      );
      expect(
        configured.origins,
        containsAll([
          'https://api.example.test',
          base,
          'https://media.example.test',
        ]),
      );
      expect(
        const AppConfig(liveMediaOrigin: 'https://media.example.test/path')
            .createLiveApi(baseUri: Uri.parse('$base/demo/')),
        isNull,
      );
    },
  );

  test('malformed, oversized and unsuccessful responses fail without partial state', () async {
    for (final response in [
      http.Response('private error body', 503),
      http.Response('x' * 524289, 200),
      reply({'version': 2, 'sessions': []}),
    ]) {
      final service = api(MockClient((_) async => response));
      await expectLater(
        service.poll(abort: Completer<void>().future),
        throwsA(anything),
      );
    }
  });

  testWidgets(
    'polling retains the last model across a transient failure and uses one request',
    (tester) async {
      var count = 0, active = 0, peak = 0;
      final service = api(
        MockClient((_) async {
          count++;
          active++;
          if (active > peak) peak = active;
          final response = count == 2
              ? http.Response('', 503)
              : reply(liveSession(state: count >= 3 ? 'ended' : 'live'));
          active--;
          return response;
        }),
      );
      final controller = LiveController(service, initialId: 'game');
      controller.start();
      await tester.pump();
      expect(controller.current!.latest!.sequence, 1);
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(
        controller.reconnecting,
        isTrue,
        reason: 'poll calls=$count loading=${controller.loading}',
      );
      expect(controller.current!.latest!.sequence, 1);
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(controller.reconnecting, isFalse);
      expect(peak, 1);
      expect(controller.current!.state, 'ended');
      final endedCount = count;
      await tester.pump(const Duration(seconds: 3));
      expect(count, endedCount);
      controller.visible(false);
      final hiddenCount = count;
      await tester.pump(const Duration(seconds: 5));
      expect(count, hiddenCount);
      controller.visible(true);
      await tester.pump();
      expect(count, hiddenCount + 1);
      controller.dispose();
    },
  );

  testWidgets(
    'an in-flight poll is not duplicated and hidden screens ignore late data',
    (tester) async {
      final blocked = Completer<http.Response>();
      var calls = 0;
      final service = api(
        MockClient((_) {
          calls++;
          return blocked.future;
        }),
      );
      final controller = LiveController(service, initialId: 'game');
      controller.start();
      await tester.pump();
      unawaited(controller.refresh());
      await tester.pump(const Duration(seconds: 2));
      expect(calls, 1);
      controller.visible(false);
      blocked.complete(reply(liveSession()));
      await tester.pump();
      expect(controller.current, isNull);
      controller.dispose();
    },
  );

  testWidgets(
    'live window replacement reuses one viewer and preserves controls on failure',
    (tester) async {
      var sequence = 1;
      final viewers = <UpdatingViewer>[];
      final service = api(
        MockClient((_) async => reply(liveSession(sequence: sequence))),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: LiveScreen(
            api: service,
            initialId: 'game',
            viewerFactory: (source) {
              final viewer = UpdatingViewer(source);
              viewers.add(viewer);
              return viewer;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(viewers, hasLength(1));
      expect(find.byKey(const Key('live-surface')), findsOneWidget);
      sequence = 2;
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(viewers.single.updates, hasLength(1));
      expect(viewers.single.disposed, isFalse);
      expect(viewers.single.value.playing, isTrue);
      viewers.single.finish(false);
      await tester.pump();
      await tester.pump();
      expect(find.textContaining('再接続中'), findsOneWidget);
      expect(find.byKey(const Key('live-surface')), findsOneWidget);
      tester
          .widget<Focus>(find.byKey(const Key('media-playback')))
          .focusNode!
          .requestFocus();
      await tester.pump();
      await tester.sendKeyEvent(LogicalKeyboardKey.space);
      await tester.pump();
      expect(viewers.single.value.playing, isFalse);
      sequence =
          1; // A stale cache pointer must not move back to an older model.
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(viewers.single.updates, hasLength(2));
      expect(viewers.single.updates.last.path, contains('/000002-'));
      viewers.single.finish(true);
      await tester.pump();
      expect(viewers, hasLength(1));
      expect(viewers.single.value.playing, isFalse);
      await tester.pumpWidget(const SizedBox.shrink());
      expect(viewers.single.disposed, isTrue);
    },
  );

  testWidgets(
    'an active revision updates displayed bookkeeping while newer metadata waits',
    (tester) async {
      var sequence = 1;
      late UpdatingViewer viewer;
      await tester.pumpWidget(
        MaterialApp(
          home: LiveScreen(
            api: api(
              MockClient((_) async => reply(liveSession(sequence: sequence))),
            ),
            initialId: 'game',
            viewerFactory: (source) => viewer = UpdatingViewer(source),
          ),
        ),
      );
      await tester.pumpAndSettle();
      for (final revision in [2, 3, 4]) {
        sequence = revision;
        await tester.pump(const Duration(seconds: 1));
        await tester.pump();
      }
      // The adapter may finish revision 2 even though the widget asks for 4.
      // Its actual draw must advance the displayed source before 4 is ready.
      expect(viewer.updates, hasLength(3));
      viewer.finish(true, at: 0);
      await tester.pump();
      await tester.pump();
      expect(viewer.value.source!.path, contains('/000002-'));
      expect(find.textContaining('0.8–1.6秒の区間'), findsOneWidget);
      viewer.finish(false, at: 1);
      viewer.finish(true, at: 2);
      await tester.pump();
      await tester.pump();
      expect(viewer.value.source!.path, contains('/000004-'));
      expect(find.textContaining('2.4–3.2秒の区間'), findsOneWidget);
      expect(viewer.disposed, isFalse);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'an intermediate model acknowledgement cannot cancel retry of a newer failed snapshot',
    (tester) async {
      var sequence = 1;
      late UpdatingViewer viewer;
      await tester.pumpWidget(
        MaterialApp(
          home: LiveScreen(
            api: api(
              MockClient((_) async => reply(liveSession(sequence: sequence))),
            ),
            initialId: 'game',
            viewerFactory: (source) => viewer = UpdatingViewer(source),
          ),
        ),
      );
      await tester.pumpAndSettle();
      sequence = 2;
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      sequence = 3;
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(viewer.updates, hasLength(2));
      viewer.finish(true, at: 0);
      viewer.finish(false, at: 1);
      await tester.pump();
      await tester.pump();
      expect(find.textContaining('再接続中'), findsOneWidget);
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(viewer.updates, hasLength(3));
      expect(viewer.updates.last.path, contains('/000003-'));
      expect(viewer.value.source!.path, contains('/000002-'));
      expect(viewer.disposed, isFalse);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets('feed entry and deep link open LIVE without new authentication', (
    tester,
  ) async {
    final service = api(
      MockClient(
        (request) async => request.url.path.endsWith('/game')
            ? reply(liveSession(sequence: 0, state: 'starting'))
            : reply({'version': 1, 'sessions': []}),
      ),
    );
    await tester.pumpWidget(
      RangleApp(
        repository: const DemoClipRepository(),
        viewerFactory: (source) => UpdatingViewer(source),
        live: service,
      ),
    );
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('LIVEを開く'));
    await tester.pumpAndSettle();
    expect(find.text('配信中のLIVEはありません'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pumpWidget(
      RangleApp(
        repository: const DemoClipRepository(),
        viewerFactory: (source) => UpdatingViewer(source),
        live: service,
        initialLiveId: 'game',
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('最初の映像を準備しています'), findsOneWidget);
    expect(find.text('バスケットボール LIVE'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
  });
  for (final direct in [false, true]) {
    for (final state in ['ready', 'waiting', 'error']) {
      testWidgets(
        '${direct ? 'direct LIVE link' : 'feed LIVE entry'} returns from $state${direct && state == 'waiting' ? ' via route back' : ''} and restores feed controls',
        (tester) async {
          tester.view.physicalSize = const Size(320, 720);
          tester.view.devicePixelRatio = 1;
          tester.view.padding = const FakeViewPadding(top: 24);
          tester.platformDispatcher.textScaleFactorTestValue = 2;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          addTearDown(tester.view.resetPadding);
          addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
          final videos = <FakeViewer>[], scenes = <FakeViewer>[];
          final liveViewers = <UpdatingViewer>[];
          final client = MockClient((request) async {
            if (state == 'error') return http.Response('{}', 404);
            final session = liveSession(sequence: state == 'ready' ? 1 : 0);
            return reply(
              request.url.path.endsWith('/game')
                  ? session
                  : {
                      'version': 1,
                      'sessions': [session],
                    },
            );
          });
          addTearDown(client.close);
          await tester.pumpWidget(
            RangleApp(
              repository: const DemoClipRepository(
                sceneUrl: 'https://example.invalid/scene.json',
              ),
              initialLiveId: direct ? 'game' : null,
              live: api(client),
              viewerFactory: (_) {
                final viewer = FakeViewer();
                videos.add(viewer);
                return viewer;
              },
              sceneFactory: (_) {
                final viewer = FakeViewer(surfaceKey: 'feed-scene');
                scenes.add(viewer);
                return viewer;
              },
              liveFactory: (source) {
                final viewer = UpdatingViewer(source);
                liveViewers.add(viewer);
                return viewer;
              },
            ),
          );
          await tester.pumpAndSettle();
          if (!direct) {
            await tester.tap(find.text('自由視点で見る'));
            await tester.pumpAndSettle();
            await tester.tap(find.byTooltip('LIVEを開く'));
            await tester.pumpAndSettle();
            expect(videos.first.disposed, isTrue);
            expect(scenes.single.disposed, isTrue);
            if (state != 'error') {
              await tester.tap(find.byType(ListTile));
              await tester.pumpAndSettle();
            }
          }
          final back = find.widgetWithText(TextButton, 'フィードに戻る');
          expect(back.hitTestable(), findsOneWidget);
          expect(
            find.ancestor(
              of: back,
              matching: find.byWidgetPredicate(
                (widget) => widget is PointerInterceptor && widget.intercepting,
              ),
            ),
            findsOneWidget,
          );
          if (state == 'ready') {
            // This also holds with a top safe inset and enlarged text: the
            // HTML surface cannot extend beneath the return button.
            expect(
              tester.getRect(find.byType(RangleHeader)).bottom,
              lessThanOrEqualTo(
                tester.getRect(find.byKey(const Key('live-surface'))).top,
              ),
            );
          }
          if (state == 'ready') {
            await tester.tap(find.byTooltip('LIVE一覧'));
            await tester.pumpAndSettle();
            expect(liveViewers.first.disposed, isTrue);
            await tester.tap(find.byType(ListTile));
            await tester.pumpAndSettle();
          }
          if (direct && state == 'waiting') {
            await tester.binding.handlePopRoute();
          } else {
            await tester.tap(back);
          }
          await tester.pumpAndSettle();
          expect(find.byType(LiveScreen), findsNothing);
          expect(liveViewers.every((viewer) => viewer.disposed), isTrue);
          expect(videos.last.disposed, isFalse);
          await tester.tap(find.byTooltip('検索'));
          await tester.pumpAndSettle();
          expect(find.byType(TextField), findsOneWidget);
          await tester.tap(find.text('キャンセル'));
          await tester.pumpAndSettle();
          await tester.tap(find.text('自由視点で見る'));
          await tester.pumpAndSettle();
          expect(find.text('動画に戻る'), findsOneWidget);
          await tester.tap(find.text('動画に戻る'));
          await tester.pumpAndSettle();
          await tester.drag(
            find.byKey(const Key('highlight-feed')),
            const Offset(0, -500),
          );
          await tester.pumpAndSettle();
          expect(find.text('バスケットボール 02'), findsOneWidget);
          expect(tester.takeException(), isNull);
          await tester.pumpWidget(const SizedBox.shrink());
        },
      );
    }
  }

  testWidgets(
    'unknown sessions stop automatic retry and retain an explicit recovery action',
    (tester) async {
      var calls = 0;
      final service = api(
        MockClient((_) async {
          calls++;
          return http.Response('{}', 404);
        }),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: LiveScreen(api: service, initialId: 'missing'),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('LIVEが見つかりません'), findsOneWidget);
      await tester.pump(const Duration(seconds: 5));
      expect(calls, 1);
      await tester.tap(find.text('再試行'));
      await tester.pumpAndSettle();
      expect(calls, 2);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'selecting a LIVE exposes list navigation and releases its viewer on return',
    (tester) async {
      final viewers = <UpdatingViewer>[];
      final service = api(
        MockClient(
          (request) async => request.url.path.endsWith('/game')
              ? reply(liveSession())
              : reply({
                  'version': 1,
                  'sessions': [liveSession()],
                }),
        ),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: LiveScreen(
            api: service,
            viewerFactory: (source) {
              final viewer = UpdatingViewer(source);
              viewers.add(viewer);
              return viewer;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byType(ListTile));
      await tester.pumpAndSettle();
      expect(viewers, hasLength(1));
      expect(find.byTooltip('LIVE一覧'), findsOneWidget);
      await tester.tap(find.byTooltip('LIVE一覧'));
      await tester.pumpAndSettle();
      expect(viewers.single.disposed, isTrue);
      expect(find.byType(ListTile), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );

  testWidgets(
    'initial asset failure retries without remounting later live snapshots',
    (tester) async {
      var sequence = 1;
      final viewers = <UpdatingViewer>[];
      final service = api(
        MockClient((_) async => reply(liveSession(sequence: sequence))),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: LiveScreen(
            api: service,
            initialId: 'game',
            viewerFactory: (source) {
              final viewer = UpdatingViewer(
                source,
                status: viewers.isEmpty
                    ? ViewerStatus.error
                    : ViewerStatus.ready,
              );
              viewers.add(viewer);
              return viewer;
            },
          ),
        ),
      );
      await tester.pumpAndSettle();
      expect(viewers, hasLength(1));
      await tester.pump(const Duration(seconds: 1));
      await tester.pumpAndSettle();
      expect(viewers, hasLength(2));
      expect(viewers.first.disposed, isTrue);
      expect(find.byKey(const Key('live-surface')), findsOneWidget);
      sequence = 2;
      await tester.pump(const Duration(seconds: 1));
      await tester.pump();
      expect(viewers, hasLength(2));
      expect(viewers.last.updates, hasLength(1));
      await tester.pumpWidget(const SizedBox.shrink());
    },
  );
}
