import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:pointer_interceptor/pointer_interceptor.dart';

import '../viewer/thumbnail_preview.dart';
import '../viewer/thumbnail_source.dart';

class EdgeSeekBar extends StatefulWidget {
  const EdgeSeekBar({
    required this.position,
    required this.duration,
    required this.onSeek,
    this.label = '再生位置',
    this.thumbnailSource,
    this.thumbnailFactory = createThumbnailSource,
    this.intercepting = false,
    this.onTogglePlayback,
    super.key,
  });

  final Duration position, duration;
  final ValueChanged<Duration>? onSeek;
  final String label;
  final Uri? thumbnailSource;
  final ThumbnailSource Function(Uri) thumbnailFactory;
  final bool intercepting;
  final VoidCallback? onTogglePlayback;

  @override
  State<EdgeSeekBar> createState() => _EdgeSeekBarState();
}

class _EdgeSeekBarState extends State<EdgeSeekBar> {
  final _focus = FocusNode();
  ThumbnailPreview? _preview;
  Duration? _target;
  bool _dragging = false, _hovering = false, _focused = false;

  bool get _enabled => widget.onSeek != null && widget.duration > Duration.zero;
  bool get _active => _dragging || _hovering || _focused;

  @override
  void didUpdateWidget(EdgeSeekBar old) {
    super.didUpdateWidget(old);
    if (old.thumbnailSource != widget.thumbnailSource) {
      _preview?.dispose();
      _preview = null;
    }
    if (!_enabled) _hide();
  }

  void _hide() {
    _target = null;
    _preview?.hide();
  }

  void _point(double x, double width) {
    if (!_enabled) return;
    final next = Duration(
      microseconds: (widget.duration.inMicroseconds * (x / width).clamp(0, 1))
          .round(),
    );
    setState(() => _target = next);
    final source = widget.thumbnailSource;
    if (source != null) {
      _preview ??= ThumbnailPreview(() => widget.thumbnailFactory(source))
        ..addListener(_changed);
      _preview!.preview(next);
    }
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  void _commit() {
    if (_target != null && _enabled) widget.onSeek!(_target!);
    setState(() {
      _dragging = false;
      if (!_hovering) _hide();
    });
  }

  Duration _stepped(int direction) {
    final step = math.min(
      5000000,
      math.max(100000, widget.duration.inMicroseconds ~/ 20),
    );
    return Duration(
      microseconds: (widget.position.inMicroseconds + direction * step).clamp(
        0,
        widget.duration.inMicroseconds,
      ),
    );
  }

  void _step(int direction) {
    if (_enabled) widget.onSeek!(_stepped(direction));
  }

  KeyEventResult _key(FocusNode node, KeyEvent event) {
    if (event is! KeyDownEvent && event is! KeyRepeatEvent) {
      return KeyEventResult.ignored;
    }
    final key = event.logicalKey;
    if (key == LogicalKeyboardKey.space || key == LogicalKeyboardKey.enter) {
      widget.onTogglePlayback?.call();
    } else if (key == LogicalKeyboardKey.arrowLeft) {
      _step(-1);
    } else if (key == LogicalKeyboardKey.arrowRight) {
      _step(1);
    } else if (key == LogicalKeyboardKey.home && _enabled) {
      widget.onSeek!(Duration.zero);
    } else if (key == LogicalKeyboardKey.end && _enabled) {
      widget.onSeek!(widget.duration);
    } else {
      return KeyEventResult.ignored;
    }
    return KeyEventResult.handled;
  }

  @override
  void dispose() {
    _preview?.dispose();
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final width = constraints.maxWidth;
      final fraction = widget.duration.inMicroseconds > 0
          ? ((_target ?? widget.position).inMicroseconds /
                    widget.duration.inMicroseconds)
                .clamp(0.0, 1.0)
          : 0.0;
      final image = _preview?.image;
      final progress = widget.duration.inMicroseconds > 0
          ? ((_dragging ? _target ?? widget.position : widget.position)
                        .inMicroseconds /
                    widget.duration.inMicroseconds)
                .clamp(0.0, 1.0)
          : 0.0;
      final previewWidth = math.min(160.0, width - 16);
      return Stack(
        clipBehavior: Clip.none,
        children: [
          if (_target != null)
            Positioned(
              bottom: 52,
              left: (fraction * width - previewWidth / 2).clamp(
                8.0,
                math.max(8, width - previewWidth - 8),
              ),
              width: previewWidth,
              child: IgnorePointer(
                child: DecoratedBox(
                  key: const Key('seek-preview'),
                  decoration: BoxDecoration(
                    color: Colors.black.withValues(alpha: .9),
                    borderRadius: BorderRadius.circular(6),
                  ),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (image != null)
                        Image.memory(
                          image,
                          key: const Key('seek-thumbnail'),
                          width: previewWidth,
                          height: previewWidth * 9 / 16,
                          fit: BoxFit.contain,
                          gaplessPlayback: false,
                          errorBuilder: (_, _, _) => const SizedBox.shrink(),
                        ),
                      Padding(
                        padding: const EdgeInsets.all(6),
                        child: Text(
                          formatMediaTime(_target!),
                          maxLines: 1,
                          style: Theme.of(context).textTheme.labelMedium,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          PointerInterceptor(
            intercepting: widget.intercepting,
            child: Semantics(
              label: widget.label,
              value:
                  '${formatMediaTime(widget.position)} / ${formatMediaTime(widget.duration)}',
              increasedValue: _enabled
                  ? '${formatMediaTime(_stepped(1))} / ${formatMediaTime(widget.duration)}'
                  : null,
              decreasedValue: _enabled
                  ? '${formatMediaTime(_stepped(-1))} / ${formatMediaTime(widget.duration)}'
                  : null,
              slider: true,
              enabled: _enabled,
              onIncrease: _enabled ? () => _step(1) : null,
              onDecrease: _enabled ? () => _step(-1) : null,
              child: Focus(
                focusNode: _focus,
                canRequestFocus: _enabled,
                onKeyEvent: _key,
                onFocusChange: (value) => setState(() => _focused = value),
                child: MouseRegion(
                  cursor: _enabled
                      ? SystemMouseCursors.click
                      : SystemMouseCursors.basic,
                  onEnter: (_) => setState(() => _hovering = true),
                  onHover: (event) => _point(event.localPosition.dx, width),
                  onExit: (_) => setState(() {
                    _hovering = false;
                    if (!_dragging) _hide();
                  }),
                  child: GestureDetector(
                    key: const Key('media-scrubber'),
                    behavior: HitTestBehavior.opaque,
                    onTapDown: _enabled
                        ? (details) {
                            _focus.requestFocus();
                            _point(details.localPosition.dx, width);
                          }
                        : null,
                    onTapUp: _enabled ? (_) => _commit() : null,
                    onTapCancel: () => setState(() {
                      if (!_dragging) _hide();
                    }),
                    onHorizontalDragStart: _enabled
                        ? (details) {
                            _dragging = true;
                            _point(details.localPosition.dx, width);
                          }
                        : null,
                    onHorizontalDragUpdate: _enabled
                        ? (details) => _point(details.localPosition.dx, width)
                        : null,
                    onHorizontalDragEnd: _enabled ? (_) => _commit() : null,
                    onHorizontalDragCancel: () => setState(() {
                      _dragging = false;
                      _hide();
                    }),
                    child: SizedBox(
                      height: 48,
                      width: double.infinity,
                      child: Align(
                        alignment: Alignment.bottomCenter,
                        child: SizedBox(
                          key: const Key('seek-track'),
                          width: double.infinity,
                          height: _active ? 5 : 3,
                          child: Stack(
                            children: [
                              Positioned.fill(
                                child: ColoredBox(
                                  color: _focused
                                      ? Colors.white54
                                      : Colors.white24,
                                ),
                              ),
                              FractionallySizedBox(
                                widthFactor: progress,
                                heightFactor: 1,
                                child: ColoredBox(
                                  color: _enabled
                                      ? Theme.of(context).colorScheme.primary
                                      : Colors.white38,
                                ),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ],
      );
    },
  );
}

String formatMediaTime(Duration time) {
  if (time.inMilliseconds < 1000) {
    return '${(time.inMilliseconds.clamp(0, 999) / 1000).toStringAsFixed(2)}秒';
  }
  final seconds = time.inSeconds.clamp(0, 359999);
  return '${seconds ~/ 60}:${(seconds % 60).toString().padLeft(2, '0')}';
}
