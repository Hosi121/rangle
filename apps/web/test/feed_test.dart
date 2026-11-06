import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/data/clip_repository.dart';
import 'package:rangle_web/main.dart';
import 'package:rangle_web/models/clip.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';

class TestRepository implements ClipRepository {
  TestRepository(this.loadClips);
  final Future<List<Clip>> Function() loadClips;

  @override
  Future<List<Clip>> load() => loadClips();
}

class FakeViewer extends ViewerAdapter {
  FakeViewer({ViewerStatus initialStatus = ViewerStatus.ready})
    : _value = ViewerValue(
        status: initialStatus,
        duration: const Duration(seconds: 20),
      );

  ViewerValue _value;
  bool disposed = false;
  int pauseCount = 0;
  Duration? lastSeek;

  @override
  ViewerValue get value => _value;

  @override
  Future<void> initialize() async {}

  void fail() {
    _value = const ViewerValue(status: ViewerStatus.error);
    notifyListeners();
  }

  @override
  Future<void> togglePlayback() async {
    _value = ViewerValue(
      status: _value.status,
      playing: !_value.playing,
      muted: _value.muted,
      position: _value.position,
      duration: _value.duration,
    );
    notifyListeners();
  }

  @override
  Future<void> seek(Duration position) async {
    lastSeek = position;
    _value = ViewerValue(
      status: _value.status,
      position: position,
      duration: _value.duration,
      playing: _value.playing,
      muted: _value.muted,
    );
    notifyListeners();
  }

  @override
  Future<void> toggleMute() async {
    _value = ViewerValue(
      status: _value.status,
      position: _value.position,
      duration: _value.duration,
      playing: _value.playing,
      muted: !_value.muted,
    );
    notifyListeners();
  }

  @override
  Future<void> pause() async {
    pauseCount++;
  }

  @override
  Widget buildView() =>
      const ColoredBox(key: Key('real-video-surface'), color: Colors.black);

  @override
  void dispose() {
    disposed = true;
    pauseCount++;
    super.dispose();
  }
}

Finder iconButton(String tooltip) => find.byWidgetPredicate(
  (widget) => widget is IconButton && widget.tooltip == tooltip,
);

Future<void> pumpApp(
  WidgetTester tester, {
  ClipRepository? repository,
  ViewerFactory? viewerFactory,
  Size size = const Size(390, 844),
}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  await tester.pumpWidget(
    RangleApp(
      repository: repository ?? const DemoClipRepository(),
      viewerFactory: viewerFactory,
    ),
  );
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('pending media has disabled playback and free-view controls', (
    tester,
  ) async {
    var created = 0;
    await pumpApp(
      tester,
      viewerFactory: (_) {
        created++;
        return FakeViewer();
      },
    );
    expect(find.text('映像を準備しています'), findsOneWidget);
    expect(find.text('バスケットボール 01'), findsOneWidget);
    expect(created, 0);
    final play = tester.widget<IconButton>(iconButton('映像準備中のため再生できません'));
    expect(play.onPressed, isNull);
    expect(tester.widget<Slider>(find.byType(Slider)).onChanged, isNull);
    final freeView = find.ancestor(
      of: find.text('自由視点は準備中'),
      matching: find.byType(OutlinedButton),
    );
    expect(tester.widget<OutlinedButton>(freeView).onPressed, isNull);
  });

  testWidgets('buttons, swipe and keyboard navigate the finite feed', (
    tester,
  ) async {
    await pumpApp(tester);
    expect(tester.widget<IconButton>(iconButton('前のハイライト')).onPressed, isNull);
    await tester.tap(iconButton('次のハイライト'));
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 02'), findsOneWidget);
    await tester.drag(
      find.byKey(const Key('highlight-feed')),
      const Offset(0, -500),
    );
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 03'), findsOneWidget);
    expect(tester.widget<IconButton>(iconButton('次のハイライト')).onPressed, isNull);
    await tester.sendKeyEvent(LogicalKeyboardKey.arrowUp);
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 02'), findsOneWidget);
    await tester.tap(iconButton('前のハイライト'));
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 01'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('desktop catalog selects a clip', (tester) async {
    await pumpApp(tester, size: const Size(1440, 900));
    await tester.tap(find.widgetWithText(ListTile, 'バスケットボール 03'));
    await tester.pumpAndSettle();
    expect(find.text('03 / 03'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('catalog load failures recover through reload', (tester) async {
    var calls = 0;
    final repository = TestRepository(() async {
      calls++;
      if (calls == 1) throw StateError('private diagnostic should stay hidden');
      return const DemoClipRepository().load();
    });
    await pumpApp(tester, repository: repository);
    expect(find.text('ハイライトを読み込めませんでした'), findsOneWidget);
    expect(find.textContaining('private diagnostic'), findsNothing);
    await tester.tap(find.text('再読み込み'));
    await tester.pumpAndSettle();
    expect(find.text('バスケットボール 01'), findsOneWidget);
    expect(calls, 2);
  });

  testWidgets('empty feed offers refresh', (tester) async {
    var calls = 0;
    await pumpApp(
      tester,
      repository: TestRepository(() async {
        calls++;
        return [];
      }),
    );
    expect(find.text('公開されたハイライトはまだありません'), findsOneWidget);
    await tester.tap(find.text('更新する'));
    await tester.pumpAndSettle();
    expect(calls, 2);
  });

  testWidgets('catalog loading is explicit', (tester) async {
    final completer = Completer<List<Clip>>();
    await pumpApp(tester, repository: TestRepository(() => completer.future));
    expect(find.text('ハイライトを読み込んでいます'), findsOneWidget);
    completer.complete([]);
    await tester.pumpAndSettle();
    expect(find.text('公開されたハイライトはまだありません'), findsOneWidget);
  });

  testWidgets('play, scrub, mute and leaving a clip release its viewer', (
    tester,
  ) async {
    final viewers = <FakeViewer>[];
    await pumpApp(
      tester,
      repository: const DemoClipRepository(
        previewUrl: 'https://example.invalid/real.mp4',
      ),
      viewerFactory: (_) {
        final viewer = FakeViewer();
        viewers.add(viewer);
        return viewer;
      },
    );
    expect(viewers, hasLength(1));
    expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
    await tester.tap(iconButton('再生'));
    await tester.pump();
    expect(viewers.single.value.playing, isTrue);
    expect(iconButton('一時停止'), findsOneWidget);
    await tester.tap(iconButton('ミュート'));
    await tester.pump();
    expect(viewers.single.value.muted, isTrue);
    await tester.tap(find.byKey(const Key('media-scrubber')));
    await tester.pump();
    expect(viewers.single.lastSeek, isNotNull);
    expect(viewers.single.lastSeek!.inMilliseconds, inInclusiveRange(1, 20000));
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    expect(viewers.single.pauseCount, 1);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.tap(iconButton('次のハイライト'));
    await tester.pumpAndSettle();
    expect(viewers.single.disposed, isTrue);
    expect(find.byKey(const Key('real-video-surface')), findsNothing);
    await tester.tap(iconButton('前のハイライト'));
    await tester.pumpAndSettle();
    expect(viewers, hasLength(2));
  });

  testWidgets('media loading is disabled and errors can retry', (tester) async {
    final viewers = <FakeViewer>[];
    await pumpApp(
      tester,
      repository: const DemoClipRepository(
        previewUrl: 'https://example.invalid/real.mp4',
      ),
      viewerFactory: (_) {
        final viewer = FakeViewer(
          initialStatus: viewers.isEmpty
              ? ViewerStatus.loading
              : ViewerStatus.ready,
        );
        viewers.add(viewer);
        return viewer;
      },
    );
    expect(find.text('映像を読み込んでいます'), findsOneWidget);
    expect(tester.widget<IconButton>(iconButton('再生')).onPressed, isNull);
    viewers.single.fail();
    await tester.pump();
    expect(find.text('映像を再生できませんでした'), findsOneWidget);
    await tester.tap(find.text('再試行'));
    await tester.pumpAndSettle();
    expect(viewers.first.disposed, isTrue);
    expect(viewers, hasLength(2));
    expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
  });

  testWidgets('small phone and landscape layouts do not overflow', (
    tester,
  ) async {
    await pumpApp(tester, size: const Size(320, 568));
    expect(tester.takeException(), isNull);
    tester.view.physicalSize = const Size(844, 390);
    await tester.pumpAndSettle();
    expect(tester.takeException(), isNull);
  });

  testWidgets('large text remains usable on a phone', (tester) async {
    tester.platformDispatcher.textScaleFactorTestValue = 2;
    addTearDown(tester.platformDispatcher.clearTextScaleFactorTestValue);
    await pumpApp(tester);
    expect(tester.takeException(), isNull);
    expect(iconButton('次のハイライト'), findsOneWidget);
  });

  testWidgets('configured preview fits the landscape layout', (tester) async {
    await pumpApp(
      tester,
      size: const Size(844, 390),
      repository: const DemoClipRepository(
        previewUrl: 'https://example.invalid/real.mp4',
      ),
      viewerFactory: (_) => FakeViewer(),
    );
    expect(find.byKey(const Key('real-video-surface')), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('pending screen meets tap target and label guidelines', (
    tester,
  ) async {
    final semantics = tester.ensureSemantics();
    try {
      await pumpApp(tester);
      await expectLater(tester, meetsGuideline(androidTapTargetGuideline));
      await expectLater(tester, meetsGuideline(labeledTapTargetGuideline));
    } finally {
      semantics.dispose();
    }
  });

  test(
    'invalid preview configuration is rejected rather than played',
    () async {
      await expectLater(
        const DemoClipRepository(previewUrl: 'file:///tmp/not-a-video.bin')
            .load(),
        throwsFormatException,
      );
    },
  );
}
