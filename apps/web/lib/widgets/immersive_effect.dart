import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'brand_art.dart';

/// Hold the entry magnification through the reveal; never pull back at the end.
double entryZoom(double charge, double reveal) {
  const peak = 1.75;
  const travel = peak - 1;
  if (reveal > 0) {
    return peak;
  }
  return 1 + travel * Curves.easeInCubic.transform(charge.clamp(0.0, 1.0));
}

class ImmersiveEffect extends StatelessWidget {
  const ImmersiveEffect({
    required this.charge,
    required this.reveal,
    required this.time,
    required this.reducedMotion,
    required this.waiting,
    super.key,
  });
  final double charge, reveal, time;
  final bool reducedMotion, waiting;
  @override
  Widget build(BuildContext context) {
    final opening = reveal > 0;
    final eye = reducedMotion && opening
        ? 1.0
        : (reveal * 2850 / 240).clamp(0.0, 1.0);
    final orbit = opening ? (reveal * 2850 / 550).clamp(.001, 1.0) : 0.0;
    final fade = 1 - ((reveal * 2850 - 2200) / 650).clamp(0.0, 1.0);
    return IgnorePointer(
      child: Opacity(
        opacity: fade,
        child: Stack(
          fit: StackFit.expand,
          children: [
            ColoredBox(
              color: const Color(0xFFEB3100).withValues(alpha: .25 * charge),
            ),
            DecoratedBox(
              decoration: BoxDecoration(
                gradient: RadialGradient(
                  colors: [
                    Colors.transparent,
                    Colors.black.withValues(alpha: .75 * charge),
                  ],
                  radius: .85,
                ),
              ),
            ),
            if (!reducedMotion)
              CustomPaint(painter: _EmberPainter(time, charge)),
            Center(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SizedBox(
                    width: math.min(
                      270,
                      MediaQuery.sizeOf(context).width * .78,
                    ),
                    height: 285,
                    child: FlameMark(
                      charge: charge,
                      eye: eye,
                      orbit: orbit,
                      time: reducedMotion ? 0 : time,
                    ),
                  ),
                  if (!opening)
                    Text(
                      waiting ? '自由視点を読み込み中' : 'この瞬間を、ひらく',
                      textAlign: TextAlign.center,
                      style: const TextStyle(
                        fontSize: 12,
                        color: Color(0xFFFFD1A6),
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _EmberPainter extends CustomPainter {
  _EmberPainter(this.time, this.charge);
  final double time, charge;
  final _paint = Paint();
  @override
  void paint(Canvas canvas, Size size) {
    for (var i = 0; i < 72; i++) {
      final phase = (time * .37 + i * .6180339) % 1;
      final x =
          ((i * 37) % 100) / 100 * size.width + math.sin(phase * 4 + i) * 18;
      final y = (1 - phase) * size.height;
      final opacity = math.sin(phase * math.pi) * (.3 + .6 * charge);
      canvas.drawRRect(
        RRect.fromRectAndRadius(
          Rect.fromLTWH(x, y, i % 3 == 0 ? 2 : 3, i % 5 == 0 ? 9 : 4),
          const Radius.circular(2),
        ),
        _paint..color = const Color(0xFFFFBC62).withValues(alpha: opacity),
      );
    }
  }

  @override
  bool shouldRepaint(_EmberPainter old) =>
      old.time != time || old.charge != charge;
}
