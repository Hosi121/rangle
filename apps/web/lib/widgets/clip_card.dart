import 'package:flutter/material.dart';

import '../models/clip.dart';
import '../viewer/viewer_adapter.dart';
import 'preview_viewer.dart';

class ClipCard extends StatelessWidget {
  const ClipCard({
    required this.clip,
    required this.active,
    required this.viewerFactory,
    this.onNext,
    super.key,
  });
  final Clip clip;
  final bool active;
  final ViewerFactory viewerFactory;
  final VoidCallback? onNext;

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      final compact =
          constraints.maxHeight < 400 ||
          MediaQuery.textScalerOf(context).scale(1) > 1.4;
      final colors = Theme.of(context).colorScheme;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Expanded(
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: colors.surfaceContainer,
                border: Border.all(color: colors.outlineVariant),
                borderRadius: BorderRadius.circular(20),
              ),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(20),
                child: Column(
                  children: [
                    if (!compact) ...[
                      Padding(
                        padding: const EdgeInsets.symmetric(
                          horizontal: 16,
                          vertical: 12,
                        ),
                        child: Row(
                          children: [
                            Icon(
                              Icons.sports_basketball_outlined,
                              size: 18,
                              color: colors.onSurfaceVariant,
                            ),
                            const SizedBox(width: 8),
                            Expanded(
                              child: Text(
                                clip.sport,
                                style: Theme.of(context).textTheme.labelLarge,
                              ),
                            ),
                            Text(
                              clip.previewUrl == null ? '映像準備中' : '通常動画',
                              style: Theme.of(context).textTheme.labelSmall,
                            ),
                          ],
                        ),
                      ),
                      const Divider(height: 1),
                    ],
                    Expanded(
                      child: clip.previewUrl != null && active
                          ? PreviewViewer(
                              key: ValueKey(clip.id),
                              source: clip.previewUrl!,
                              factory: viewerFactory,
                            )
                          : Column(
                              children: [
                                Expanded(
                                  child: compact
                                      ? Center(
                                          child: Text(
                                            '映像を準備しています',
                                            style: Theme.of(context)
                                                .textTheme
                                                .titleSmall,
                                          ),
                                        )
                                      : ViewerMessage(
                                          icon: Icons.video_library_outlined,
                                          title: '映像を準備しています',
                                          description:
                                              '公開されたハイライトが\nここに表示されます。',
                                          action: onNext == null
                                              ? null
                                              : OutlinedButton.icon(
                                                  onPressed: onNext,
                                                  icon: const Icon(
                                                    Icons.arrow_downward,
                                                    size: 18,
                                                  ),
                                                  label: const Text('次の項目を見る'),
                                                ),
                                        ),
                                ),
                                if (!compact) const _PendingControls(),
                              ],
                            ),
                    ),
                  ],
                ),
              ),
            ),
          ),
          Padding(
            padding: EdgeInsets.fromLTRB(
              4,
              compact ? 4 : 16,
              4,
              compact ? 0 : 12,
            ),
            child: compact
                ? Row(
                    children: [
                      Expanded(
                        child: Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Semantics(
                              header: true,
                              child: Text(
                                clip.title,
                                maxLines: 2,
                                overflow: TextOverflow.ellipsis,
                                style: Theme.of(context).textTheme.titleMedium,
                              ),
                            ),
                            Text(
                              clip.previewUrl == null ? '映像準備中' : '通常動画',
                              style: Theme.of(context).textTheme.labelSmall,
                            ),
                          ],
                        ),
                      ),
                      const IconButton(
                        tooltip: '自由視点は準備中',
                        onPressed: null,
                        icon: Icon(Icons.threed_rotation),
                      ),
                    ],
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Semantics(
                        header: true,
                        child: Text(
                          clip.title,
                          style: Theme.of(context).textTheme.titleLarge
                              ?.copyWith(fontWeight: FontWeight.w700),
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        clip.description,
                        style: Theme.of(context).textTheme.bodySmall?.copyWith(
                          height: 1.6,
                          color: colors.onSurfaceVariant,
                        ),
                      ),
                      const SizedBox(height: 12),
                      SizedBox(
                        width: double.infinity,
                        child: OutlinedButton.icon(
                          onPressed: null,
                          icon: const Icon(Icons.threed_rotation, size: 18),
                          label: const Text('自由視点は準備中'),
                        ),
                      ),
                    ],
                  ),
          ),
        ],
      );
    },
  );
}

class _PendingControls extends StatelessWidget {
  const _PendingControls();

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
    child: Column(
      children: [
        Semantics(
          label: '映像準備中のため再生位置を変更できません',
          child: const Slider(value: 0, onChanged: null),
        ),
        const Row(
          children: [
            IconButton.filledTonal(
              tooltip: '映像準備中のため再生できません',
              onPressed: null,
              icon: Icon(Icons.play_arrow),
            ),
            SizedBox(width: 12),
            Expanded(
              child: Text('未公開', maxLines: 1, overflow: TextOverflow.ellipsis),
            ),
          ],
        ),
      ],
    ),
  );
}
