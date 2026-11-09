import 'package:flutter/material.dart';

import 'brand_art.dart';

class RangleHeader extends StatelessWidget implements PreferredSizeWidget {
  const RangleHeader({
    required this.title,
    this.onBack,
    this.actions = const [],
    super.key,
  });
  final String title;
  final VoidCallback? onBack;
  final List<Widget> actions;
  @override
  Size get preferredSize => const Size.fromHeight(64);
  @override
  Widget build(BuildContext context) => AppBar(
    backgroundColor: Colors.black.withValues(alpha: .85),
    surfaceTintColor: Colors.transparent,
    toolbarHeight: 64,
    leading: onBack == null ? null : BackButton(onPressed: onBack),
    titleSpacing: 4,
    title: Row(
      children: [
        const Flexible(child: RangleWordmark(width: 184)),
        const SizedBox(width: 12),
        Text(
          title,
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w700,
            color: Color(0xFFFF954A),
          ),
        ),
      ],
    ),
    actions: actions,
  );
}
