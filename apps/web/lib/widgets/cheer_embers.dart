import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

/// Small, overlapping bursts anchored to the applause button. No input layer.
class CheerEmbers extends StatefulWidget {
  const CheerEmbers({required this.trigger, required this.child, super.key});
  final int trigger;
  final Widget child;
  @override
  State<CheerEmbers> createState() => _CheerEmbersState();
}

class _CheerEmbersState extends State<CheerEmbers>
    with SingleTickerProviderStateMixin {
  late final Ticker _ticker = createTicker(_tick);
  final _bursts = <_Burst>[];
  Duration _clock = Duration.zero, _base = Duration.zero;
  int _seed = 0;
  void _tick(Duration elapsed) {
    setState(() {
      _clock = _base + elapsed;
      _bursts.removeWhere((b) => (_clock - b.born).inMilliseconds >= 850);
    });
    if (_bursts.isEmpty) _ticker.stop();
  }

  @override
  void didUpdateWidget(CheerEmbers old) {
    super.didUpdateWidget(old);
    if (widget.trigger == old.trigger ||
        MediaQuery.disableAnimationsOf(context)) {
      return;
    }
    if (_bursts.length == 4) _bursts.removeAt(0);
    _bursts.add(_Burst(_clock, ++_seed));
    if (!_ticker.isActive) {
      _base = _clock;
      _ticker.start();
    }
  }

  @override
  void dispose() {
    _ticker.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Stack(
    clipBehavior: Clip.none,
    alignment: Alignment.topCenter,
    children: [
      widget.child,
      if (_bursts.isNotEmpty && !MediaQuery.disableAnimationsOf(context))
        Positioned(
          top: -64,
          left: -40,
          width: 128,
          height: 112,
          child: IgnorePointer(
            child: CustomPaint(
              key: const Key('cheer-embers'),
              painter: _EmbersPainter(List.of(_bursts), _clock),
            ),
          ),
        ),
    ],
  );
}

class _Burst {
  _Burst(this.born, this.seed);
  final Duration born;
  final int seed;
}

class _EmbersPainter extends CustomPainter {
  _EmbersPainter(this.bursts, this.clock);
  final List<_Burst> bursts;
  final Duration clock;
  @override
  void paint(Canvas canvas, Size size) {
    final origin = Offset(size.width / 2, size.height - 28);
    for (final burst in bursts) {
      final progress = ((clock - burst.born).inMicroseconds / 850000).clamp(
        0.0,
        1.0,
      );
      final random = math.Random(burst.seed);
      if (progress < .3) {
        canvas.drawCircle(
          origin,
          8 + progress * 16,
          Paint()
            ..color = const Color(0xFFFF6500)
                .withValues(alpha: .15 * (1 - progress / .3))
            ..maskFilter = const MaskFilter.blur(BlurStyle.normal, 5),
        );
      }
      for (var i = 0; i < 10; i++) {
        final spread = random.nextDouble() * 2 - 1;
        final rise = 18 + random.nextDouble() * 30;
        final radius = .8 + random.nextDouble() * 1.5;
        final phase = random.nextDouble() * math.pi * 2;
        final x =
            origin.dx +
            spread * 28 * progress +
            math.sin(progress * 4 + phase) * 3 * progress;
        final y =
            origin.dy -
            rise * Curves.easeOut.transform(progress) -
            math.sin(progress * math.pi) * 6;
        final alpha =
            (1 - progress) * (.6 + .2 * math.sin(progress * 4 + phase).abs());
        final color = Color.lerp(
          const Color(0xFFFFE6A0),
          const Color(0xFFFF4C00),
          progress,
        )!;
        final paint = Paint()
          ..color = color.withValues(alpha: alpha)
          ..strokeWidth = radius * (1 - .5 * progress)
          ..strokeCap = StrokeCap.round;
        canvas.drawLine(
          Offset(x, y + 1.5 + progress * 2),
          Offset(x + spread * 2, y),
          paint,
        );
      }
    }
  }

  @override
  bool shouldRepaint(_EmbersPainter old) => true;
}
