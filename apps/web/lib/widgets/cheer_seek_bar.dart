import 'dart:math' as math;

import 'package:flutter/material.dart';

import 'edge_seek_bar.dart';

class CheerSeekBar extends StatelessWidget {
  const CheerSeekBar({
    required this.bins,
    required this.position,
    required this.duration,
    required this.onSeek,
    required this.intercepting,
    this.thumbnailSource,
    this.onTogglePlayback,
    super.key,
  });
  final List<int> bins;
  final Duration position, duration;
  final ValueChanged<Duration>? onSeek;
  final bool intercepting;
  final Uri? thumbnailSource;
  final VoidCallback? onTogglePlayback;
  @override
  Widget build(BuildContext context) => Stack(
    alignment: Alignment.bottomCenter,
    children: [
      Positioned(
        left: 0,
        right: 0,
        bottom: 0,
        height: 7,
        child: IgnorePointer(child: CustomPaint(painter: _HeatPainter(bins))),
      ),
      EdgeSeekBar(
        position: position,
        duration: duration,
        onSeek: onSeek,
        label: '声援が集まる場面へ移動',
        intercepting: intercepting,
        thumbnailSource: thumbnailSource,
        onTogglePlayback: onTogglePlayback,
      ),
    ],
  );
}

class _HeatPainter extends CustomPainter {
  _HeatPainter(this.bins);
  final List<int> bins;
  @override
  void paint(Canvas canvas, Size size) {
    if (bins.isEmpty) return;
    final maxCount = math.max(1, bins.reduce(math.max));
    for (var i = 0; i < bins.length; i++) {
      final intensity = math.log(1 + bins[i]) / math.log(1 + maxCount);
      canvas.drawRect(
        Rect.fromLTWH(
          i * size.width / bins.length,
          0,
          size.width / bins.length,
          size.height,
        ),
        Paint()
          ..color = const Color(0xFFFF6500)
              .withValues(alpha: bins[i] == 0 ? 0.06 : 0.2 + 0.8 * intensity),
      );
    }
  }

  @override
  bool shouldRepaint(_HeatPainter old) => true;
}
