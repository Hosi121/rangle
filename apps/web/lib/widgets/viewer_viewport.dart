import 'dart:math' as math;

import 'package:flutter/material.dart';

/// Constrains the whole viewing surface, including Scaffold notifications.
class ViewerViewport extends StatelessWidget {
  const ViewerViewport({required this.child, super.key});

  final Widget child;

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: Colors.black,
    child: LayoutBuilder(
      builder: (context, constraints) {
        final padding = MediaQuery.paddingOf(context);
        final height = constraints.maxHeight - padding.vertical;
        final desktop = constraints.maxWidth >= 700 && height >= 600;
        final width = desktop
            ? math.min(520.0, height * 9 / 16)
            : constraints.maxWidth;
        return Center(
          child: SizedBox(
            width: width,
            height: constraints.maxHeight,
            child: child,
          ),
        );
      },
    ),
  );
}
