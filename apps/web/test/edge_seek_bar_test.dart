import 'dart:convert';
import 'dart:ui' show PointerDeviceKind;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/thumbnail_source.dart';
import 'package:rangle_web/widgets/edge_seek_bar.dart';

class _Frames implements ThumbnailSource {
  final requested = <Duration>[];
  bool unavailable = false, disposed = false;
  @override
  Future<Uint8List?> frame(Duration position) async {
    requested.add(position);
    return unavailable
        ? null
        : base64Decode(
            'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j5WQAAAAASUVORK5CYII=',
          );
  }

  @override
  void dispose() => disposed = true;
}

Future<void> _show(
  WidgetTester tester,
  _Frames frames, {
  ValueChanged<Duration>? onSeek,
  Uri? source,
  double scale = 1,
}) => tester.pumpWidget(
  MaterialApp(
    home: MediaQuery(
      data: MediaQueryData(
        size: const Size(320, 640),
        textScaler: TextScaler.linear(scale),
      ),
      child: Scaffold(
        body: Stack(
          children: [
            Positioned(
              left: 0,
              right: 0,
              bottom: 0,
              child: EdgeSeekBar(
                position: const Duration(seconds: 2),
                duration: const Duration(seconds: 20),
                onSeek: onSeek,
                thumbnailSource:
                    source ?? Uri.parse('https://example.invalid/clip.webm'),
                thumbnailFactory: (_) => frames,
              ),
            ),
          ],
        ),
      ),
    ),
  ),
);

void main() {
  testWidgets('thin edge timeline previews the hovered frame without seeking', (
    tester,
  ) async {
    final frames = _Frames();
    final seeks = <Duration>[];
    await _show(tester, frames, onSeek: seeks.add);
    final track = tester.getRect(find.byKey(const Key('seek-track')));
    final target = tester.getRect(find.byKey(const Key('media-scrubber')));
    expect(track.height, 3);
    expect(track.bottom, target.bottom);
    expect(target.height, 48);
    expect(frames.requested, isEmpty);
    final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
    await mouse.addPointer(location: const Offset(1, 1));
    await mouse.moveTo(target.center);
    await tester.pump(const Duration(milliseconds: 80));
    await tester.pumpAndSettle();
    expect(frames.requested.single, const Duration(seconds: 10));
    expect(find.byKey(const Key('seek-thumbnail')), findsOneWidget);
    expect(find.text('0:10'), findsOneWidget);
    expect(seeks, isEmpty);
    expect(
      tester
          .widget<FractionallySizedBox>(find.byType(FractionallySizedBox))
          .widthFactor,
      .1,
    );
    expect(tester.getRect(find.byKey(const Key('seek-track'))).height, 5);
    await mouse.moveTo(const Offset(1, 1));
    await tester.pump();
    expect(find.byKey(const Key('seek-preview')), findsNothing);
    expect(tester.getRect(find.byKey(const Key('seek-track'))).height, 3);
    await mouse.removePointer();
  });

  testWidgets('touch drag previews then commits once on release', (
    tester,
  ) async {
    final frames = _Frames();
    final seeks = <Duration>[];
    await _show(tester, frames, onSeek: seeks.add, scale: 2);
    final rect = tester.getRect(find.byKey(const Key('media-scrubber')));
    final gesture = await tester.startGesture(
      Offset(rect.left + rect.width / 4, rect.center.dy),
    );
    await gesture.moveTo(Offset(rect.left + rect.width * .75, rect.center.dy));
    await tester.pump(const Duration(milliseconds: 80));
    expect(seeks, isEmpty);
    expect(find.byKey(const Key('seek-preview')), findsOneWidget);
    await gesture.up();
    await tester.pump();
    expect(seeks, [const Duration(seconds: 15)]);
    expect(find.byKey(const Key('seek-preview')), findsNothing);
    expect(tester.takeException(), isNull);
  });

  testWidgets(
    'time-only fallback, keyboard seeking and decoder cleanup remain usable',
    (tester) async {
      final frames = _Frames()..unavailable = true;
      final seeks = <Duration>[];
      await _show(tester, frames, onSeek: seeks.add);
      final mouse = await tester.createGesture(kind: PointerDeviceKind.mouse);
      await mouse.addPointer(location: const Offset(1, 1));
      await mouse.moveTo(
        tester.getCenter(find.byKey(const Key('media-scrubber'))),
      );
      await tester.pump(const Duration(milliseconds: 80));
      expect(find.text('0:10'), findsOneWidget);
      expect(find.byKey(const Key('seek-thumbnail')), findsNothing);
      Focus.of(tester.element(find.byKey(const Key('media-scrubber'))))
          .requestFocus();
      await tester.pump();
      await tester.sendKeyEvent(LogicalKeyboardKey.arrowRight);
      await tester.sendKeyEvent(LogicalKeyboardKey.home);
      await tester.sendKeyEvent(LogicalKeyboardKey.end);
      expect(seeks, [
        const Duration(seconds: 3),
        Duration.zero,
        const Duration(seconds: 20),
      ]);
      await _show(
        tester,
        frames,
        onSeek: seeks.add,
        source: Uri.parse('https://example.invalid/other.webm'),
      );
      expect(frames.disposed, isTrue);
      await mouse.removePointer();
    },
  );
}
