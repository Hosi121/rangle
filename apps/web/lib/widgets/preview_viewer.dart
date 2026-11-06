import 'dart:async';

import 'package:flutter/material.dart';

import '../viewer/viewer_adapter.dart';

class PreviewViewer extends StatefulWidget {
  const PreviewViewer({required this.source, required this.factory, super.key});

  final Uri source;
  final ViewerFactory factory;

  @override
  State<PreviewViewer> createState() => _PreviewViewerState();
}

class _PreviewViewerState extends State<PreviewViewer>
    with WidgetsBindingObserver {
  late ViewerAdapter _adapter;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _create();
  }

  void _create() {
    _adapter = widget.factory(widget.source);
    unawaited(_adapter.initialize());
  }

  void _retry() {
    setState(() {
      _adapter.dispose();
      _create();
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) unawaited(_adapter.pause());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _adapter.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: _adapter,
    builder: (context, _) {
      final value = _adapter.value;
      final ready = value.status == ViewerStatus.ready;
      return Column(
        children: [
          Expanded(
            child: switch (value.status) {
              ViewerStatus.loading => const ViewerMessage(
                icon: Icons.video_library_outlined,
                title: '映像を読み込んでいます',
                description: 'まもなくプレビューを表示します。',
              ),
              ViewerStatus.error => ViewerMessage(
                icon: Icons.error_outline,
                title: '映像を再生できませんでした',
                description: '接続状況や映像の公開状態をご確認ください。',
                action: OutlinedButton.icon(
                  onPressed: _retry,
                  icon: const Icon(Icons.refresh),
                  label: const Text('再試行'),
                ),
              ),
              ViewerStatus.ready => Semantics(
                label: '通常動画のプレビュー',
                child: _adapter.buildView(),
              ),
            },
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(12, 8, 12, 12),
            child: Column(
              children: [
                if (ready && value.buffering) const Text('映像を読み込んでいます'),
                Semantics(
                  label: '再生位置',
                  child: Slider(
                    key: const Key('media-scrubber'),
                    value: ready
                        ? value.position.inMilliseconds.toDouble().clamp(
                            0,
                            value.duration.inMilliseconds.toDouble(),
                          )
                        : 0,
                    max: value.duration.inMilliseconds > 0
                        ? value.duration.inMilliseconds.toDouble()
                        : 1,
                    onChanged: ready && value.duration > Duration.zero
                        ? (value) => unawaited(
                            _adapter.seek(
                              Duration(milliseconds: value.round()),
                            ),
                          )
                        : null,
                    semanticFormatterCallback: (value) =>
                        _formatTime(Duration(milliseconds: value.round())),
                  ),
                ),
                Row(
                  children: [
                    IconButton.filledTonal(
                      tooltip: value.playing ? '一時停止' : '再生',
                      onPressed: ready
                          ? () => unawaited(_adapter.togglePlayback())
                          : null,
                      icon: Icon(
                        value.playing ? Icons.pause : Icons.play_arrow,
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Text(
                        '${_formatTime(value.position)} / ${_formatTime(value.duration)}',
                        style: const TextStyle(
                          fontFeatures: [FontFeature.tabularFigures()],
                        ),
                      ),
                    ),
                    IconButton(
                      tooltip: value.muted ? 'ミュート解除' : 'ミュート',
                      onPressed: ready
                          ? () => unawaited(_adapter.toggleMute())
                          : null,
                      icon: Icon(
                        value.muted ? Icons.volume_off : Icons.volume_up,
                      ),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ],
      );
    },
  );
}

String _formatTime(Duration time) {
  final seconds = time.inSeconds.clamp(0, 359999);
  return '${seconds ~/ 60}:${(seconds % 60).toString().padLeft(2, '0')}';
}

class ViewerMessage extends StatelessWidget {
  const ViewerMessage({
    required this.icon,
    required this.title,
    required this.description,
    this.action,
    super.key,
  });

  final IconData icon;
  final String title;
  final String description;
  final Widget? action;

  @override
  Widget build(BuildContext context) => Center(
    child: SingleChildScrollView(
      padding: const EdgeInsets.all(24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 48, color: Theme.of(context).colorScheme.outline),
          const SizedBox(height: 24),
          Text(
            title,
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.titleMedium,
          ),
          const SizedBox(height: 12),
          Text(
            description,
            textAlign: TextAlign.center,
            style: Theme.of(context).textTheme.bodyMedium?.copyWith(
              color: Theme.of(context).colorScheme.onSurfaceVariant,
              height: 1.7,
            ),
          ),
          if (action != null) ...[const SizedBox(height: 24), action!],
        ],
      ),
    ),
  );
}
