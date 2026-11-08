import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../models/clip.dart';
import '../widgets/feed_sheet.dart';
import 'share_clip.dart';

Future<void> showClipShare(
  BuildContext context, {
  required Clip clip,
  required ClipShareAction share,
  required double width,
}) => showFeedSheet(
  context,
  title: 'ハイライトを共有',
  width: width,
  builder: (_) => _ShareSheet(clip: clip, share: share),
);

class _ShareSheet extends StatefulWidget {
  const _ShareSheet({required this.clip, required this.share});

  final Clip clip;
  final ClipShareAction share;

  @override
  State<_ShareSheet> createState() => _ShareSheetState();
}

class _ShareSheetState extends State<_ShareSheet> {
  bool _busy = false;
  String? _message;
  bool _failed = false;

  Future<void> _run({required bool copy}) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _message = null;
      _failed = false;
    });
    try {
      final ShareResult result;
      if (copy) {
        await Clipboard.setData(
          ClipboardData(text: clipLink(Uri.base, widget.clip.id).toString()),
        );
        result = ShareResult.copied;
      } else {
        result = await widget.share(widget.clip);
      }
      if (!mounted) return;
      setState(() {
        _message = switch (result) {
          ShareResult.copied => 'リンクをコピーしました',
          ShareResult.shared => '共有しました',
          ShareResult.cancelled => null,
        };
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _failed = true;
        _message = '共有できませんでした。リンクを選択してコピーできます。';
      });
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(widget.clip.title, style: theme.textTheme.titleLarge),
        const SizedBox(height: 16),
        DecoratedBox(
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(12),
          ),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: SelectableText(
              clipLink(Uri.base, widget.clip.id).toString(),
              key: const Key('share-link'),
              style: theme.textTheme.bodyMedium,
            ),
          ),
        ),
        const SizedBox(height: 20),
        FilledButton.icon(
          onPressed: _busy ? null : () => _run(copy: true),
          icon: const Icon(Icons.link),
          label: const Text('リンクをコピー'),
        ),
        const SizedBox(height: 8),
        OutlinedButton.icon(
          onPressed: _busy ? null : () => _run(copy: false),
          icon: const Icon(Icons.ios_share),
          label: const Text('端末の共有を開く'),
        ),
        if (_message != null)
          Padding(
            padding: const EdgeInsets.only(top: 12),
            child: Semantics(
              liveRegion: true,
              child: Text(
                _message!,
                style: TextStyle(
                  color: _failed
                      ? theme.colorScheme.error
                      : theme.colorScheme.primary,
                ),
              ),
            ),
          ),
      ],
    );
  }
}
