// Vector geometry from the supplied rangle-editable.svg; no raster approximation.
import 'dart:math' as math;

import 'package:flutter/material.dart';

final _paths = <String, Path>{
  'flame-silhouette': Path()
    ..moveTo(385, 679)
    ..lineTo(390, 659)
    ..cubicTo(364, 603, 372, 545, 426, 452)
    ..lineTo(549, 337)
    ..lineTo(501, 438)
    ..cubicTo(551, 405, 590, 364, 607, 311)
    ..cubicTo(619, 274, 608, 255, 580, 255)
    ..lineTo(793, 100)
    ..cubicTo(734, 197, 714, 246, 750, 344)
    ..lineTo(740, 372)
    ..cubicTo(774, 349, 801, 318, 819, 277)
    ..cubicTo(831, 337, 847, 408, 850, 441)
    ..cubicTo(851, 483, 837, 539, 817, 578)
    ..lineTo(759, 626)
    ..lineTo(803, 617)
    ..cubicTo(775, 681, 751, 718, 706, 747)
    ..cubicTo(670, 770, 569, 805, 490, 845)
    ..lineTo(501, 815)
    ..cubicTo(498, 792, 480, 761, 455, 735)
    ..lineTo(442, 718)
    ..lineTo(402, 667)
    ..close(),
  'flame-upper-facet': Path()
    ..moveTo(793, 100)
    ..cubicTo(734, 197, 714, 246, 750, 344)
    ..lineTo(722, 341)
    ..lineTo(629, 423)
    ..lineTo(472, 508)
    ..lineTo(390, 659)
    ..cubicTo(414, 567, 503, 497, 598, 431)
    ..lineTo(669, 282)
    ..close(),
  'flame-brow-facet': Path()
    ..moveTo(390, 659)
    ..lineTo(472, 508)
    ..lineTo(629, 423)
    ..lineTo(722, 341)
    ..lineTo(740, 346)
    ..lineTo(740, 372)
    ..cubicTo(774, 349, 801, 318, 819, 277)
    ..cubicTo(814, 325, 801, 362, 779, 394)
    ..lineTo(689, 443)
    ..lineTo(721, 440)
    ..lineTo(689, 476)
    ..lineTo(809, 446)
    ..lineTo(653, 528)
    ..lineTo(554, 559)
    ..close(),
  'flame-bottom-facet': Path()
    ..moveTo(442, 718)
    ..lineTo(448, 661)
    ..lineTo(601, 681)
    ..lineTo(568, 693)
    ..close()
    ..moveTo(490, 845)
    ..cubicTo(561, 782, 652, 733, 752, 682)
    ..cubicTo(738, 706, 721, 729, 706, 747)
    ..cubicTo(670, 770, 569, 805, 490, 845)
    ..close(),
  'eye-upper-cut': Path()
    ..moveTo(385, 679)
    ..cubicTo(418, 610, 464, 552, 518, 517)
    ..cubicTo(540, 501, 560, 497, 590, 487)
    ..lineTo(721, 440)
    ..lineTo(689, 476)
    ..lineTo(809, 446)
    ..cubicTo(731, 490, 642, 522, 558, 552)
    ..cubicTo(488, 578, 428, 623, 385, 679)
    ..close(),
  'eye-white': Path()
    ..moveTo(439, 643)
    ..lineTo(484, 623)
    ..lineTo(484, 603)
    ..lineTo(527, 580)
    ..cubicTo(543, 624, 568, 639, 604, 639)
    ..cubicTo(651, 638, 690, 588, 678, 535)
    ..lineTo(713, 526)
    ..cubicTo(717, 539, 716, 558, 711, 571)
    ..cubicTo(723, 556, 733, 537, 740, 518)
    ..lineTo(770, 505)
    ..cubicTo(753, 555, 733, 599, 711, 624)
    ..lineTo(590, 725)
    ..cubicTo(597, 710, 601, 694, 602, 681)
    ..close(),
  'iris': Path()
    ..moveTo(527, 580)
    ..lineTo(558, 552)
    ..lineTo(634, 526)
    ..lineTo(678, 535)
    ..cubicTo(690, 588, 651, 638, 604, 639)
    ..cubicTo(568, 639, 543, 624, 527, 580)
    ..close(),
  'iris-shadow': Path()
    ..moveTo(527, 580)
    ..lineTo(558, 552)
    ..lineTo(634, 526)
    ..lineTo(678, 535)
    ..lineTo(681, 554)
    ..lineTo(631, 569)
    ..lineTo(541, 609)
    ..close(),
  'pupil': Path()
    ..moveTo(558, 552)
    ..lineTo(616, 532)
    ..lineTo(634, 526)
    ..cubicTo(643, 536, 647, 551, 645, 565)
    ..cubicTo(642, 590, 624, 605, 603, 605)
    ..cubicTo(574, 605, 553, 584, 558, 552)
    ..close(),
  'pupil-highlight': Path()
    ..moveTo(610, 554)
    ..lineTo(636, 546)
    ..lineTo(623, 570)
    ..lineTo(597, 579)
    ..close(),
  'orbit-right': Path()
    ..moveTo(862, 476)
    ..cubicTo(955, 458, 993, 483, 950, 525)
    ..cubicTo(920, 555, 844, 594, 759, 626)
    ..cubicTo(825, 589, 893, 545, 914, 510)
    ..cubicTo(933, 481, 902, 479, 862, 476)
    ..close(),
  'orbit-left': Path()
    ..moveTo(370, 651)
    ..cubicTo(300, 690, 270, 731, 302, 746)
    ..cubicTo(337, 763, 437, 742, 568, 693)
    ..cubicTo(454, 724, 369, 744, 338, 725)
    ..cubicTo(321, 714, 336, 684, 370, 651)
    ..close(),
  'spark-right-upper': Path()
    ..moveTo(885, 422)
    ..lineTo(904, 400)
    ..lineTo(992, 374)
    ..close(),
  'spark-right-lower': Path()
    ..moveTo(877, 458)
    ..lineTo(915, 435)
    ..lineTo(979, 416)
    ..lineTo(964, 429)
    ..close(),
  'spark-left': Path()
    ..moveTo(283, 662)
    ..lineTo(296, 647)
    ..lineTo(354, 629)
    ..close(),
  'R': Path()
    ..moveTo(157, 885)
    ..lineTo(273, 885)
    ..cubicTo(299, 885, 315, 899, 315, 922)
    ..lineTo(315, 947)
    ..cubicTo(315, 967, 303, 977, 282, 981)
    ..lineTo(319, 1034)
    ..lineTo(268, 1034)
    ..lineTo(232, 981)
    ..lineTo(202, 981)
    ..lineTo(202, 1034)
    ..lineTo(157, 1034)
    ..close()
    ..moveTo(202, 918)
    ..lineTo(202, 949)
    ..lineTo(262, 949)
    ..cubicTo(268, 949, 271, 947, 271, 941)
    ..lineTo(271, 927)
    ..cubicTo(271, 921, 268, 918, 262, 918)
    ..close()
    ..fillType = PathFillType.evenOdd,
  'A': Path()
    ..moveTo(322, 1034)
    ..lineTo(385, 890)
    ..quadraticBezierTo(387, 885, 392, 885)
    ..lineTo(433, 885)
    ..quadraticBezierTo(438, 885, 440, 891)
    ..lineTo(502, 1034)
    ..lineTo(429, 1034)
    ..lineTo(397, 994)
    ..lineTo(440, 994)
    ..lineTo(412, 929)
    ..lineTo(369, 1034)
    ..close(),
  'N': Path()
    ..moveTo(514, 885)
    ..lineTo(550, 885)
    ..quadraticBezierTo(557, 885, 562, 892)
    ..lineTo(623, 975)
    ..lineTo(623, 885)
    ..lineTo(665, 885)
    ..lineTo(665, 1034)
    ..lineTo(628, 1034)
    ..quadraticBezierTo(621, 1034, 616, 1027)
    ..lineTo(556, 947)
    ..lineTo(556, 1034)
    ..lineTo(514, 1034)
    ..close(),
  'G': Path()
    ..moveTo(823, 885)
    ..lineTo(804, 920)
    ..lineTo(737, 920)
    ..quadraticBezierTo(724, 920, 724, 932)
    ..lineTo(724, 986)
    ..quadraticBezierTo(724, 998, 736, 998)
    ..lineTo(776, 998)
    ..quadraticBezierTo(783, 998, 783, 992)
    ..lineTo(783, 975)
    ..lineTo(746, 975)
    ..lineTo(760, 942)
    ..lineTo(824, 942)
    ..lineTo(824, 997)
    ..quadraticBezierTo(824, 1034, 789, 1034)
    ..lineTo(724, 1034)
    ..quadraticBezierTo(682, 1034, 682, 993)
    ..lineTo(682, 928)
    ..quadraticBezierTo(682, 885, 726, 885)
    ..close(),
  'L': Path()
    ..moveTo(842, 885)
    ..lineTo(884, 885)
    ..lineTo(884, 998)
    ..lineTo(963, 998)
    ..lineTo(950, 1034)
    ..lineTo(842, 1034)
    ..close(),
  'E': Path()
    ..moveTo(976, 885)
    ..lineTo(1101, 885)
    ..lineTo(1082, 920)
    ..lineTo(1018, 920)
    ..lineTo(1018, 943)
    ..lineTo(1094, 943)
    ..lineTo(1076, 977)
    ..lineTo(1018, 977)
    ..lineTo(1018, 998)
    ..lineTo(1102, 998)
    ..lineTo(1087, 1034)
    ..lineTo(976, 1034)
    ..close(),
};

// Fixed SVG-local paints and shaders are shared across sizes and animations.
// They are never mutated after creation; Canvas applies each draw's transform.
final _whitePaint = Paint()..color = Colors.white;
final _unchargedPaint = Paint()..color = const Color(0xFF8D3D0A);
final _closedEyePath = Path()
  ..moveTo(440, 625)
  ..quadraticBezierTo(557, 601, 721, 513);
final _closedEyePaint = Paint()
  ..color = const Color(0xFF32120B)
  ..style = PaintingStyle.stroke
  ..strokeWidth = 16
  ..strokeCap = StrokeCap.round;
final _paints = <String, Paint>{
  for (final id in _paths.keys.where((id) => id.length != 1))
    id: _createPaint(id),
};
Paint _createPaint(String id) {
  if (id == 'eye-white' || id == 'eye-upper-cut' || id == 'pupil-highlight') {
    return _whitePaint;
  }
  if (id == 'pupil') {
    return Paint()..color = const Color(0xFF080909);
  }
  if (id == 'iris-shadow') {
    return Paint()..color = const Color(0x80D94A00);
  }
  if (id == 'flame-brow-facet') {
    return Paint()..color = const Color(0xFFEE4C00);
  }
  final path = _paths[id]!;
  final colors = id.startsWith('flame-') && id != 'flame-silhouette'
      ? const [Color(0xFFFF7100), Color(0xFFDB3000)]
      : const [Color(0xFFFF9500), Color(0xFFFF6500), Color(0xFFFB4000)];
  return Paint()
    ..shader = LinearGradient(
      begin: Alignment.topLeft,
      end: Alignment.bottomRight,
      colors: colors,
    ).createShader(path.getBounds());
}

class RangleWordmark extends StatelessWidget {
  const RangleWordmark({this.width = 154, super.key});
  final double width;
  @override
  Widget build(BuildContext context) => Semantics(
    label: 'RANGLE',
    header: true,
    child: SizedBox(
      width: width,
      height: width * 149 / 945,
      child: CustomPaint(painter: _WordmarkPainter()),
    ),
  );
}

class _WordmarkPainter extends CustomPainter {
  @override
  void paint(Canvas canvas, Size size) {
    canvas.scale(size.width / 945);
    canvas.translate(-157, -885);
    for (final id in ['R', 'A', 'N', 'G', 'L', 'E']) {
      canvas.drawPath(_paths[id]!, _whitePaint);
    }
  }

  @override
  bool shouldRepaint(_WordmarkPainter old) => false;
}

class FlameMark extends StatelessWidget {
  const FlameMark({
    required this.charge,
    required this.eye,
    required this.orbit,
    required this.time,
    super.key,
  });
  final double charge, eye, orbit, time;
  @override
  Widget build(BuildContext context) =>
      CustomPaint(painter: _FlamePainter(charge, eye, orbit, time));
}

class _FlamePainter extends CustomPainter {
  _FlamePainter(this.charge, this.eye, this.orbit, this.time);
  final double charge, eye, orbit, time;
  void draw(Canvas canvas, Iterable<String> ids) {
    for (final id in ids) {
      canvas.drawPath(_paths[id]!, _paints[id]!);
    }
  }

  @override
  void paint(Canvas canvas, Size size) {
    canvas.save();
    canvas.scale(size.width / 709, size.height / 745);
    canvas.translate(-283, -100);
    const flames = [
      'flame-silhouette',
      'flame-upper-facet',
      'flame-brow-facet',
      'flame-bottom-facet',
    ];
    canvas.save();
    canvas.translate(620, 800);
    canvas.skew(math.sin(time * 7) * .015, 0);
    canvas.scale(1, 1 + math.sin(time * 9) * .012);
    canvas.translate(-620, -800);
    for (final id in flames) {
      canvas.drawPath(_paths[id]!, _unchargedPaint);
    }
    canvas.save();
    canvas.clipRect(
      Rect.fromLTRB(270, 845 - 745 * charge.clamp(0, 1), 1010, 850),
    );
    draw(canvas, flames);
    canvas.restore();
    canvas.restore();
    if (eye > 0) {
      canvas.save();
      canvas.translate(0, 565);
      canvas.scale(1, eye.clamp(.01, 1));
      canvas.translate(0, -565);
      draw(canvas, [
        'eye-upper-cut',
        'eye-white',
        'iris',
        'iris-shadow',
        'pupil',
        'pupil-highlight',
      ]);
      canvas.restore();
    } else {
      canvas.drawPath(_closedEyePath, _closedEyePaint);
    }
    if (orbit > 0) {
      final burst = orbit < .3
          ? .55 + orbit / .3 * 1.75
          : 2.3 - (orbit - .3) / .7;
      canvas.save();
      canvas.translate(637, 570);
      canvas.scale(burst);
      canvas.translate(-637, -570);
      draw(canvas, [
        'orbit-right',
        'orbit-left',
        'spark-right-upper',
        'spark-right-lower',
        'spark-left',
      ]);
      canvas.restore();
    }
    canvas.restore();
  }

  @override
  bool shouldRepaint(_FlamePainter old) => true;
}
