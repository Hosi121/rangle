import 'dart:async';

import 'package:flutter/material.dart';

import '../community/community_api.dart';
import '../models/publisher_account.dart';
import '../widgets/rangle_header.dart';
import '../widgets/viewer_viewport.dart';
import 'publisher_api.dart';
import 'publisher_forms.dart';

class PublisherDeletionScreen extends StatefulWidget {
  const PublisherDeletionScreen({
    required this.api,
    required this.account,
    this.operator = false,
    this.onAuthenticationExpired,
    super.key,
  });
  final PublisherApi api;
  final PublisherAccount account;
  final bool operator;
  final VoidCallback? onAuthenticationExpired;
  @override
  State<PublisherDeletionScreen> createState() =>
      _PublisherDeletionScreenState();
}

class _PublisherDeletionScreenState extends State<PublisherDeletionScreen> {
  PublisherDeletion? _progress;
  bool _requested = false, _loading = true, _busy = false;
  String? _error;
  Timer? _poll;
  int _epoch = 0;
  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  @override
  void dispose() {
    _epoch++;
    _poll?.cancel();
    super.dispose();
  }

  void _failed(Object error) {
    if (error is CommunityFailure && error.code == 'not_authenticated') {
      widget.onAuthenticationExpired?.call();
    }
    _error = publisherMessage(error);
  }

  void _schedule() {
    _poll?.cancel();
    if (_progress?.pending == true && _error == null) {
      _poll = Timer(const Duration(seconds: 2), () => unawaited(_load()));
    }
  }

  Future<void> _load() async {
    if (_busy) return;
    final epoch = ++_epoch;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      if (widget.operator) {
        final progress = await widget.api.deletionStatus(widget.account.id);
        if (!mounted || epoch != _epoch) return;
        setState(() {
          _progress = progress;
          _requested = progress.requested;
        });
      } else {
        final requested = await widget.api.deletionRequested(widget.account.id);
        if (!mounted || epoch != _epoch) return;
        setState(() => _requested = requested);
      }
    } catch (e) {
      if (mounted && epoch == _epoch) setState(() => _failed(e));
    } finally {
      if (mounted && epoch == _epoch) {
        setState(() => _loading = false);
        _schedule();
      }
    }
  }

  Future<void> _confirm() async {
    if (_busy || _loading) return;
    _poll?.cancel();
    setState(() => _busy = true);
    final field = TextEditingController();
    final requesting = !_requested;
    try {
      final accepted = await showPublisherForm(
        context,
        title: widget.operator
            ? '団体を削除する'
            : requesting
            ? '削除を依頼する'
            : '削除依頼を取り消す',
        submitLabel: widget.operator
            ? '削除を開始'
            : requesting
            ? '依頼する'
            : '依頼を取り消す',
        onDispose: field.dispose,
        fields: (busy, _) => Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text('確認のため「${widget.account.id}」を入力してください。'),
            const SizedBox(height: 16),
            TextFormField(
              controller: field,
              enabled: !busy,
              decoration: const InputDecoration(labelText: '団体ID'),
              autocorrect: false,
              validator: (value) =>
                  value == widget.account.id ? null : '団体IDが一致しません。',
            ),
          ],
        ),
        submit: () async {
          if (widget.operator) {
            final progress = await widget.api.deletePublisher(
              widget.account.id,
            );
            if (mounted) setState(() => _progress = progress);
          } else {
            final requested = await widget.api.requestDeletion(
              widget.account.id,
              requesting,
            );
            if (mounted) setState(() => _requested = requested);
          }
        },
      );
      if (!mounted) return;
      if (accepted) setState(() => _error = null);
    } finally {
      if (mounted) {
        setState(() => _busy = false);
        _schedule();
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final pending = _progress?.pending == true;
    final deleted = _progress?.state == 'deleted';
    return ViewerViewport(
      child: Scaffold(
        appBar: RangleHeader(
          title: '団体の削除',
          onBack: _busy ? null : () => Navigator.pop(context, _progress?.state),
        ),
        body: SafeArea(
          child: Center(
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 520),
              child: ListView(
                padding: const EdgeInsets.all(24),
                children: [
                  Text(
                    widget.account.name,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                  const SizedBox(height: 8),
                  Text(widget.account.id),
                  const SizedBox(height: 24),
                  if (widget.operator && (pending || deleted)) ...[
                    Semantics(liveRegion: true, child: Text(_progress!.label)),
                    if (pending) ...[
                      const SizedBox(height: 16),
                      const Text('画面を閉じても処理状態は保存されます。'),
                    ],
                  ] else ...[
                    const Text(
                      '団体を削除すると、団体の動画・モデルは見られなくなります。声援・コメント・個人の履歴は削除済み動画として残ります。',
                    ),
                    const SizedBox(height: 16),
                    if (widget.operator)
                      const Text('削除開始後は取り消せません。')
                    else
                      Text(
                        _requested
                            ? '運営へ削除を依頼しています。実行前は取り消せます。'
                            : '運営へ削除を依頼します。確認が完了するまでは利用できます。',
                      ),
                    const SizedBox(height: 24),
                    OutlinedButton(
                      onPressed: _busy || _loading ? null : _confirm,
                      child: Text(
                        widget.operator
                            ? '削除を開始'
                            : _requested
                            ? '削除依頼を取り消す'
                            : '削除を依頼',
                      ),
                    ),
                  ],
                  if (_loading || pending && _error == null) ...[
                    const SizedBox(height: 16),
                    const LinearProgressIndicator(),
                  ],
                  if (_error != null) ...[
                    const SizedBox(height: 16),
                    Semantics(
                      liveRegion: true,
                      child: Text(
                        _error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                    TextButton(
                      onPressed: _loading || _busy ? null : _load,
                      child: const Text('進捗を再確認'),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
