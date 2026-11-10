import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/widgets/brand_art.dart';
import 'package:rangle_web/widgets/cheer_embers.dart';
import 'package:rangle_web/widgets/immersive_effect.dart';

Future<Uint8List> flamePixels(
  WidgetTester tester, {
  required double charge,
  required double eye,
  required double orbit,
  required double time,
  required Size size,
}) async {
  await tester.pumpWidget(
    Directionality(
      textDirection: TextDirection.ltr,
      child: Center(
        child: SizedBox.fromSize(
          size: size,
          child: FlameMark(charge: charge, eye: eye, orbit: orbit, time: time),
        ),
      ),
    ),
  );
  final painter = tester.widget<CustomPaint>(find.byType(CustomPaint)).painter!;
  return (await tester.runAsync(() async {
    final recorder = ui.PictureRecorder();
    painter.paint(Canvas(recorder), size);
    final picture = recorder.endRecording();
    final image = await picture.toImage(
      size.width.toInt(),
      size.height.toInt(),
    );
    picture.dispose();
    final data = await image.toByteData(format: ui.ImageByteFormat.rawRgba);
    image.dispose();
    return data!.buffer.asUint8List();
  }))!;
}

class _ParticleCanvas implements Canvas {
  final lines = <(Offset, Offset, Color, double, StrokeCap)>[];
  int circles = 0;
  @override
  void drawLine(Offset p1, Offset p2, Paint paint) {
    // Snapshot mutable Paint properties as Canvas does for recorded draws.
    lines.add((p1, p2, paint.color, paint.strokeWidth, paint.strokeCap));
    expect(
      paint.maskFilter,
      isNull,
      reason: 'glow blur must not leak to embers',
    );
  }

  @override
  void drawCircle(Offset c, double radius, Paint paint) => circles++;
  @override
  dynamic noSuchMethod(Invocation invocation) =>
      throw UnsupportedError('${invocation.memberName}');
}

_ParticleCanvas _particles(WidgetTester tester) {
  final painter = tester
      .widget<CustomPaint>(find.byKey(const Key('cheer-embers')))
      .painter!;
  final canvas = _ParticleCanvas();
  painter.paint(canvas, const Size(128, 112));
  return canvas;
}

Widget cheers(int trigger, {bool reduced = false}) => Directionality(
  textDirection: TextDirection.ltr,
  child: MediaQuery(
    data: MediaQueryData(disableAnimations: reduced),
    child: Center(
      child: CheerEmbers(
        trigger: trigger,
        child: const SizedBox(width: 48, height: 48),
      ),
    ),
  ),
);

void main() {
  testWidgets(
    'cached flame resources remain independent across poses and sizes',
    (tester) async {
      const size = Size(270, 285);
      final closed = await flamePixels(
        tester,
        charge: .6,
        eye: 0,
        orbit: 0,
        time: .75,
        size: size,
      );
      final open = await flamePixels(
        tester,
        charge: 1,
        eye: 1,
        orbit: .3,
        time: 2,
        size: size,
      );
      expect(listEquals(closed, open), isFalse);
      await flamePixels(
        tester,
        charge: .2,
        eye: .4,
        orbit: .7,
        time: 3.4,
        size: const Size(128, 135),
      );
      final restored = await flamePixels(
        tester,
        charge: .6,
        eye: 0,
        orbit: 0,
        time: .75,
        size: size,
      );
      expect(listEquals(closed, restored), isTrue);
    },
  );

  testWidgets(
    'cheer draws stay deterministic, bounded to four, and expire at 850ms',
    (tester) async {
      await tester.pumpWidget(cheers(0));
      for (var trigger = 1; trigger <= 6; trigger++) {
        await tester.pumpWidget(cheers(trigger));
        await tester.pump(const Duration(milliseconds: 10));
        final first = _particles(tester);
        final repeated = _particles(tester);
        expect(first.lines.length, (trigger < 4 ? trigger : 4) * 10);
        expect(repeated.lines, first.lines);
        expect(first.lines.every((line) => line.$5 == StrokeCap.round), isTrue);
      }
      // The newest burst was born at 50ms. It remains at age 849ms, then ends.
      await tester.pump(const Duration(milliseconds: 839));
      expect(_particles(tester).lines.length, 10);
      await tester.pump(const Duration(milliseconds: 1));
      expect(find.byKey(const Key('cheer-embers')), findsNothing);
      await tester.pumpWidget(cheers(7));
      await tester.pump(const Duration(milliseconds: 10));
      expect(_particles(tester).lines.length, 10);
      await tester.pumpWidget(const SizedBox());
      await tester.pump(const Duration(seconds: 2));
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'reduced motion hides active cheers without creating new bursts',
    (tester) async {
      await tester.pumpWidget(cheers(0));
      await tester.pumpWidget(cheers(1));
      await tester.pump(const Duration(milliseconds: 100));
      final before = _particles(tester).lines;
      await tester.pumpWidget(cheers(2, reduced: true));
      await tester.pump(const Duration(milliseconds: 100));
      expect(find.byKey(const Key('cheer-embers')), findsNothing);
      await tester.pumpWidget(cheers(2));
      final after = _particles(tester).lines;
      expect(after.length, 10);
      expect(after, isNot(before));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('cheer-embers')), findsNothing);
    },
  );

  testWidgets(
    'immersive reduced motion omits particles and freezes flame time',
    (tester) async {
      Widget effect(bool reduced) => Directionality(
        textDirection: TextDirection.ltr,
        child: MediaQuery(
          data: const MediaQueryData(size: Size(390, 844)),
          child: ImmersiveEffect(
            charge: 1,
            reveal: .04,
            time: 12.5,
            reducedMotion: reduced,
            waiting: false,
          ),
        ),
      );
      await tester.pumpWidget(effect(false));
      expect(find.byType(CustomPaint), findsNWidgets(2));
      var flame = tester.widget<FlameMark>(find.byType(FlameMark));
      expect(flame.time, 12.5);
      expect(flame.eye, .04 * 2850 / 240);
      final orbit = flame.orbit;
      await tester.pumpWidget(effect(true));
      expect(find.byType(CustomPaint), findsOneWidget);
      flame = tester.widget<FlameMark>(find.byType(FlameMark));
      expect(flame.time, 0);
      expect(flame.eye, 1);
      expect(flame.orbit, orbit);
    },
  );
}
