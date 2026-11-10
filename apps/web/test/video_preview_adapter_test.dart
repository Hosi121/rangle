import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/widgets/preview_viewer.dart';
import 'package:video_player/video_player.dart';

/// Exercises the production adapter without network, browser, or native media.
class ControlledVideoController extends VideoPlayerController {
  ControlledVideoController()
    : super.networkUrl(Uri.parse('https://example.invalid/preview.mp4'));

  final initialized = Completer<void>();
  final released = Completer<void>();
  int disposeCount = 0;
  int pauseCount = 0;
  int initializeCount = 0;
  final commands = <String>[];
  Object? playFailure;
  String? playStreamFailure;
  bool muteFailure = false;
  Duration? nativePosition;

  @override
  Future<Duration?> get position async => nativePosition ?? value.position;

  @override
  Future<void> setVolume(double volume) async {
    commands.add('volume:$volume');
    if (muteFailure) throw StateError('Muting unavailable');
    value = value.copyWith(volume: volume);
  }

  @override
  Future<void> setLooping(bool looping) async {
    commands.add('loop:$looping');
    value = value.copyWith(isLooping: looping);
  }

  @override
  Future<void> seekTo(Duration position) async {
    commands.add('seek:${position.inMicroseconds}');
    value = value.copyWith(position: position);
  }

  @override
  Future<void> play() async {
    commands.add('play');
    if (playFailure != null) throw playFailure!;
    if (playStreamFailure != null) {
      value = VideoPlayerValue.erroneous(playStreamFailure!);
    } else {
      value = value.copyWith(isPlaying: true);
    }
  }

  @override
  Future<void> initialize() {
    initializeCount++;
    return initialized.future;
  }

  @override
  Future<void> pause() async {
    pauseCount++;
    value = value.copyWith(isPlaying: false);
  }

  @override
  Future<void> dispose() async {
    disposeCount++;
    await released.future;
    await super.dispose();
  }

  void becomeReady() {
    value = const VideoPlayerValue(
      duration: Duration(seconds: 20),
      isInitialized: true,
    );
    initialized.complete();
  }
}

void main() {
  testWidgets(
    'native geometry changes refresh the retained surface without a playback change',
    (tester) async {
      final controller = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(controller);
      await tester.pumpWidget(
        MaterialApp(
          home: PreviewViewer(
            source: Uri.parse('https://example.invalid/preview.mp4'),
            factory: (_) => adapter,
            showControls: false,
          ),
        ),
      );
      controller.becomeReady();
      await tester.pumpAndSettle();
      expect(
        tester.widget<AspectRatio>(find.byType(AspectRatio)).aspectRatio,
        1,
      );
      controller.value = controller.value.copyWith(size: const Size(1600, 900));
      await tester.pump();
      expect(
        tester.widget<AspectRatio>(find.byType(AspectRatio)).aspectRatio,
        16 / 9,
      );
      expect(adapter.value.aspectRatio, 16 / 9);
      var updates = 0;
      adapter.addListener(() => updates++);
      controller.value = controller.value.copyWith(rotationCorrection: 90);
      await tester.pump();
      expect(updates, 1);
      expect(adapter.value.rotationCorrection, 90);
      await tester.pumpWidget(const SizedBox());
      controller.released.complete();
      await tester.pump();
    },
  );
  testWidgets(
    'native events with unchanged viewer state do not notify, while time and control changes do',
    (tester) async {
      final controller = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(controller);
      final initialized = adapter.initialize();
      controller.becomeReady();
      await tester.pump();
      await initialized;
      var updates = 0;
      adapter.addListener(() => updates++);
      for (var i = 0; i < 60; i++) {
        controller.value = controller.value.copyWith(
          isLooping: !controller.value.isLooping,
        );
      }
      expect(updates, 0);
      controller.value = controller.value.copyWith(
        position: const Duration(milliseconds: 560),
      );
      expect(updates, 1);
      controller.value = controller.value.copyWith(isBuffering: true);
      expect(updates, 2);
      controller.value = controller.value.copyWith(isPlaying: false);
      expect(updates, 3);
      controller.value = controller.value.copyWith(volume: 1);
      expect(updates, 4);
      adapter.dispose();
      controller.released.complete();
      await tester.pump();
    },
  );
  testWidgets(
    'handoff captures native currentTime rather than the older periodic controller position',
    (tester) async {
      final controller = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(controller);
      final initialized = adapter.initialize();
      controller.becomeReady();
      await tester.pump();
      await initialized;
      await adapter.togglePlayback();
      controller.value = controller.value.copyWith(
        position: const Duration(milliseconds: 500),
      );
      controller.nativePosition = const Duration(milliseconds: 560);
      final captured = await adapter.capturePlayback();
      expect(captured.position, const Duration(milliseconds: 560));
      expect(captured.playing, isFalse);
      adapter.dispose();
      controller.released.complete();
      await tester.pump();
    },
  );
  for (final playing in [false, true]) {
    testWidgets(
      'native restoration seeks before first ready and preserves playing=$playing',
      (tester) async {
        final controller = ControlledVideoController();
        final adapter = VideoPreviewAdapter.withController(controller);
        final readyPositions = <Duration>[];
        adapter.addListener(() {
          if (adapter.value.status == ViewerStatus.ready) {
            readyPositions.add(adapter.value.position);
          }
        });
        final restored = adapter.restorePlayback(
          PlaybackState(const Duration(milliseconds: 560), playing),
        );
        final initialized = adapter.initialize();
        controller.becomeReady();
        await tester.pump();
        await initialized;
        expect(await restored, isTrue);
        expect(readyPositions, isNotEmpty);
        expect(
          readyPositions.every((p) => p == const Duration(milliseconds: 560)),
          isTrue,
        );
        expect(
          controller.commands.indexOf('seek:560000'),
          greaterThanOrEqualTo(0),
        );
        expect(adapter.value.playing, playing);
        if (playing) {
          expect(
            controller.commands.indexOf('play'),
            greaterThan(controller.commands.indexOf('seek:560000')),
          );
        } else {
          expect(controller.commands, isNot(contains('play')));
        }
        adapter.dispose();
        controller.released.complete();
        await tester.pump();
      },
    );
  }

  testWidgets(
    'blocked restoration retains its source time on the manual fallback player',
    (tester) async {
      final controller = ControlledVideoController()
        ..playFailure = PlatformException(code: 'NotAllowedError');
      final manual = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(
        controller,
        retryController: () => manual,
      );
      final restored = adapter.restorePlayback(
        const PlaybackState(Duration(milliseconds: 760), true),
      );
      final initialized = adapter.initialize();
      controller.becomeReady();
      await tester.pump();
      manual.becomeReady();
      await tester.pump();
      await initialized;
      expect(await restored, isTrue);
      expect(adapter.value.status, ViewerStatus.ready);
      expect(adapter.value.position, const Duration(milliseconds: 760));
      expect(adapter.value.playing, isFalse);
      expect(manual.commands, contains('seek:760000'));
      adapter.dispose();
      controller.released.complete();
      manual.released.complete();
      await tester.pump();
    },
  );
  testWidgets(
    'the latest pre-initialization restoration wins without autoplaying the stale request',
    (tester) async {
      final controller = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(controller);
      final obsolete = adapter.restorePlayback(
        const PlaybackState(Duration(milliseconds: 560), true),
      );
      final latest = adapter.restorePlayback(
        const PlaybackState(Duration(milliseconds: 200), false),
      );
      expect(await obsolete, isFalse);
      final initialized = adapter.initialize();
      controller.becomeReady();
      await tester.pump();
      await initialized;
      expect(await latest, isTrue);
      expect(controller.commands, isNot(contains('seek:560000')));
      expect(controller.commands, isNot(contains('play')));
      expect(adapter.value.position, const Duration(milliseconds: 200));
      expect(adapter.value.playIntent, isFalse);
      adapter.dispose();
      controller.released.complete();
      await tester.pump();
    },
  );

  testWidgets('a real media failure during restoration remains an error', (
    tester,
  ) async {
    final controller = ControlledVideoController()
      ..playFailure = PlatformException(code: 'MEDIA_ERR_NETWORK');
    final adapter = VideoPreviewAdapter.withController(controller);
    final restored = adapter.restorePlayback(
      const PlaybackState(Duration(milliseconds: 560), true),
    );
    final initialized = adapter.initialize();
    controller.becomeReady();
    await tester.pump();
    await initialized;
    expect(await restored, isFalse);
    expect(adapter.value.status, ViewerStatus.error);
    adapter.dispose();
    controller.released.complete();
    await tester.pump();
  });

  testWidgets(
    'native loop position wraps retain one initialized player and suppress short waiting pulses',
    (tester) async {
      final controller = ControlledVideoController();
      var replacements = 0, adapters = 0;
      final adapter = VideoPreviewAdapter.withController(
        controller,
        retryController: () {
          replacements++;
          return ControlledVideoController();
        },
      );
      await tester.pumpWidget(
        MaterialApp(
          home: PreviewViewer(
            source: Uri.parse('https://example.invalid/preview.mp4'),
            factory: (_) {
              adapters++;
              return adapter;
            },
          ),
        ),
      );
      controller.becomeReady();
      await tester.pumpAndSettle();
      final playerState = tester.state(find.byType(VideoPlayer));
      for (var i = 0; i < 4; i++) {
        controller.value = controller.value.copyWith(
          position: const Duration(milliseconds: 19960),
        );
        await tester.pump();
        controller.value = controller.value.copyWith(
          position: Duration.zero,
          isBuffering: true,
        );
        await tester.pump(const Duration(milliseconds: 50));
        expect(find.text('映像を読み込んでいます'), findsNothing);
        controller.value = controller.value.copyWith(
          position: const Duration(milliseconds: 40),
          isBuffering: false,
        );
        await tester.pump();
        expect(tester.state(find.byType(VideoPlayer)), same(playerState));
      }
      expect(adapters, 1);
      expect(controller.initializeCount, 1);
      expect(controller.disposeCount, 0);
      expect(replacements, 0);
      expect(controller.commands, ['volume:0.0', 'loop:true', 'play']);
      expect(adapter.value.status, ViewerStatus.ready);
      expect(adapter.value.playing, isTrue);
      controller.released.complete();
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
      expect(controller.disposeCount, 1);
    },
  );

  testWidgets(
    'autoplay policy fallback resumes through the video surface without a play button',
    (tester) async {
      final blocked = ControlledVideoController()
        ..playFailure = PlatformException(code: 'NotAllowedError');
      final manual = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(
        blocked,
        retryController: () => manual,
      );
      await tester.pumpWidget(
        MaterialApp(
          home: PreviewViewer(
            source: Uri.parse('https://example.invalid/preview.mp4'),
            factory: (_) => adapter,
          ),
        ),
      );
      blocked.becomeReady();
      await tester.pump();
      manual.becomeReady();
      await tester.pumpAndSettle();
      expect(adapter.value.playing, isFalse);
      expect(find.text('タップで再生'), findsOneWidget);
      expect(find.byIcon(Icons.play_arrow), findsNothing);
      await tester.tap(find.byKey(const Key('video-playback-surface')));
      await tester.pump();
      expect(adapter.value.playing, isTrue);
      expect(find.text('タップで再生'), findsNothing);
      blocked.released.complete();
      manual.released.complete();
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    },
  );

  testWidgets('autoplay confirms mute and looping before starting', (
    tester,
  ) async {
    final controller = ControlledVideoController();
    final adapter = VideoPreviewAdapter.withController(controller);
    final initialization = adapter.initialize();
    controller.becomeReady();
    await initialization;
    expect(controller.commands, ['volume:0.0', 'loop:true', 'play']);
    expect(adapter.value.status, ViewerStatus.ready);
    expect(adapter.value.muted, isTrue);
    expect(adapter.value.playing, isTrue);
    adapter.dispose();
  });

  testWidgets('policy rejection recreates a muted manual player', (
    tester,
  ) async {
    final blocked = ControlledVideoController()
      ..playFailure = PlatformException(
        code: 'NotAllowedError',
        message: 'Policy blocked play',
      );
    final manual = ControlledVideoController();
    var replacements = 0;
    final adapter = VideoPreviewAdapter.withController(
      blocked,
      retryController: () {
        replacements++;
        return manual;
      },
    );
    final initialization = adapter.initialize();
    blocked.becomeReady();
    await tester.pump();
    expect(replacements, 1);
    expect(blocked.disposeCount, 1);
    manual.becomeReady();
    await initialization;
    expect(adapter.value.status, ViewerStatus.ready);
    expect(adapter.value.muted, isTrue);
    expect(adapter.value.playing, isFalse);
    expect(manual.commands, ['volume:0.0', 'loop:true']);
    await adapter.togglePlayback();
    expect(adapter.value.playing, isTrue);
    adapter.dispose();
  });

  testWidgets(
    'muting failure prevents autoplay and preserves manual controls',
    (tester) async {
      final controller = ControlledVideoController()..muteFailure = true;
      final adapter = VideoPreviewAdapter.withController(controller);
      final initialization = adapter.initialize();
      controller.becomeReady();
      await initialization;
      expect(controller.commands, ['volume:0.0']);
      expect(adapter.value.status, ViewerStatus.ready);
      expect(adapter.value.playing, isFalse);
      await adapter.togglePlayback();
      expect(adapter.value.playing, isTrue);
      adapter.dispose();
    },
  );

  testWidgets(
    'web policy stream error also falls back without hiding media failures',
    (tester) async {
      final blocked = ControlledVideoController()
        ..playStreamFailure =
            "play() failed because the user didn't interact with the document first.";
      final manual = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(
        blocked,
        retryController: () => manual,
      );
      final initialization = adapter.initialize();
      blocked.becomeReady();
      await initialization;
      manual.becomeReady();
      await tester.pump();
      expect(adapter.value.status, ViewerStatus.ready);
      expect(adapter.value.playing, isFalse);
      expect(manual.commands, isNot(contains('play')));
      manual.value = const VideoPlayerValue.erroneous(
        'Network or decoding failed',
      );
      await tester.pump();
      expect(adapter.value.status, ViewerStatus.error);
      expect(manual.disposeCount, 1);
      adapter.dispose();
    },
  );

  testWidgets('non-policy autoplay failure remains a real media error', (
    tester,
  ) async {
    final controller = ControlledVideoController()
      ..playFailure = PlatformException(
        code: 'NetworkError',
        message: 'Network unavailable',
      );
    var replacements = 0;
    final adapter = VideoPreviewAdapter.withController(
      controller,
      retryController: () {
        replacements++;
        return ControlledVideoController();
      },
    );
    final initialization = adapter.initialize();
    controller.becomeReady();
    await initialization;
    expect(adapter.value.status, ViewerStatus.error);
    expect(replacements, 0);
    await tester.pump();
    expect(controller.disposeCount, 1);
    adapter.dispose();
  });

  testWidgets(
    'timeout releases media once and late readiness cannot restore it',
    (tester) async {
      final controller = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(controller);
      unawaited(adapter.initialize());
      await tester.pump(const Duration(seconds: 16));
      expect(adapter.value.status, ViewerStatus.error);
      expect(controller.disposeCount, 1);
      controller.becomeReady();
      await tester.pump();
      expect(adapter.value.status, ViewerStatus.error);
      adapter.dispose();
      await tester.pump();
      expect(controller.disposeCount, 1);
    },
  );

  testWidgets(
    'leaving during initialization releases media and ignores completion',
    (tester) async {
      final controller = ControlledVideoController();
      final adapter = VideoPreviewAdapter.withController(controller);
      var notifications = 0;
      adapter.addListener(() => notifications++);
      final initialization = adapter.initialize();
      adapter.dispose();
      await tester.pump();
      expect(controller.disposeCount, 1);
      controller.becomeReady();
      await initialization;
      expect(notifications, 0);
      expect(controller.disposeCount, 1);
    },
  );

  testWidgets('native media error pauses and releases an initialized player', (
    tester,
  ) async {
    final controller = ControlledVideoController();
    final adapter = VideoPreviewAdapter.withController(controller);
    final initialization = adapter.initialize();
    controller.becomeReady();
    await initialization;
    expect(adapter.value.status, ViewerStatus.ready);
    controller.value = controller.value.copyWith(
      errorDescription: 'unsupported media',
    );
    await tester.pump();
    expect(adapter.value.status, ViewerStatus.error);
    expect(controller.pauseCount, 1);
    expect(controller.disposeCount, 1);
    adapter.dispose();
    await tester.pump();
    expect(controller.disposeCount, 1);
    controller.released.complete();
    await tester.pump();
  });
}
