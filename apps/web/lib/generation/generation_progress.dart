import 'package:flutter/material.dart';

import 'generation_api.dart';

class GenerationProgressIndicator extends StatelessWidget {
  const GenerationProgressIndicator({
    required this.job,
    this.unavailable = false,
    super.key,
  });
  final GenerationJob job;
  final bool unavailable;

  @override
  Widget build(BuildContext context) {
    if (job.isLive || !job.active && job.state != 'succeeded') {
      return const SizedBox.shrink();
    }
    final progress = job.progress;
    final completed = job.state == 'succeeded';
    final measured = job.state == 'running' && progress?.stage == 'training';
    final value = completed
        ? 1.0
        : measured
        ? progress!.fraction
        : null;
    final label = completed
        ? '生成完了'
        : switch (job.state) {
            'submitting' => '生成を準備中',
            'queued' => '生成の準備待ち',
            'cancelling' => '中止を確認中',
            _ => switch (progress?.stage) {
              'training' => '学習 ${(progress!.fraction! * 100).floor()}%',
              'finalizing' => '結果を仕上げています',
              _ => '学習状況を確認しています',
            },
          };
    final eta = measured && !unavailable ? progress?.etaLabel : null;
    return Padding(
      padding: const EdgeInsets.only(top: 12, bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (!completed) ...[Text(label), const SizedBox(height: 8)],
          TickerMode(
            enabled: !MediaQuery.disableAnimationsOf(context),
            child: LinearProgressIndicator(value: value, semanticsLabel: label),
          ),
          if (!completed &&
              job.state == 'running' &&
              progress?.stage != 'finalizing') ...[
            const SizedBox(height: 8),
            Text(
              eta ??
                  (progress?.stale == true || unavailable
                      ? '進捗の更新を待っています'
                      : measured
                      ? '残り時間を更新しています'
                      : '残り時間は学習開始後に表示します'),
            ),
          ],
        ],
      ),
    );
  }
}
