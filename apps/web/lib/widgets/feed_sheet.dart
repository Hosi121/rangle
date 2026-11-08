import 'package:flutter/material.dart';

import 'viewer_modal.dart';

/// Keeps feed panels within the player's width, including on desktop.
Future<void> showFeedSheet(
  BuildContext context, {
  required String title,
  required double width,
  required WidgetBuilder builder,
}) => showViewerBottomSheet<void>(
  context: context,
  constraints: BoxConstraints(maxWidth: width),
  backgroundColor: Theme.of(context).colorScheme.surfaceContainer,
  clipBehavior: Clip.antiAlias,
  builder: (context) => SafeArea(
    top: false,
    child: ConstrainedBox(
      constraints: BoxConstraints(
        maxHeight: MediaQuery.sizeOf(context).height * .8,
      ),
      child: SizedBox(
        width: double.infinity,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 8, 0),
              child: Row(
                children: [
                  Expanded(
                    child: Semantics(
                      header: true,
                      child: Text(
                        title,
                        style: Theme.of(context).textTheme.titleMedium,
                      ),
                    ),
                  ),
                  IconButton(
                    tooltip: '閉じる',
                    onPressed: () => Navigator.pop(context),
                    icon: const Icon(Icons.close),
                  ),
                ],
              ),
            ),
            Flexible(
              child: SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(20, 16, 20, 24),
                child: builder(context),
              ),
            ),
          ],
        ),
      ),
    ),
  ),
);
