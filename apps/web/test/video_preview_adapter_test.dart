import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:video_player/video_player.dart';

/// Exercises the production adapter without network, browser, or native media.
class ControlledVideoController extends VideoPlayerController {
  ControlledVideoController()
    : super.networkUrl(Uri.parse('https://example.invalid/preview.mp4'));

  final initialized = Completer<void>();
  final released = Completer<void>();
  int disposeCount = 0;
  int pauseCount = 0;

  @override
  Future<void> initialize() => initialized.future;

  @override
  Future<void> pause() async {
    pauseCount++;
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
