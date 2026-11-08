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
  final commands = <String>[];
  Object? playFailure;
  String? playStreamFailure;
  bool muteFailure = false;

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
  Future<void> initialize() => initialized.future;

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
