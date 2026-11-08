import 'package:flutter/material.dart';

import '../widgets/viewer_modal.dart';

import 'account_controller.dart';
import 'account_dialog.dart';
import 'community_api.dart';

Future<void> showComments(
  BuildContext context,
  String clip,
  AccountController? account,
) async {
  if (account == null) {
    ScaffoldMessenger.of(context)
        .showSnackBar(const SnackBar(content: Text('現在コメントを利用できません。')));
    return;
  }
  await showViewerBottomSheet<void>(
    context: context,
    builder: (_) => _CommentsSheet(clip: clip, account: account),
  );
}

class _CommentsSheet extends StatefulWidget {
  const _CommentsSheet({required this.clip, required this.account});
  final String clip;
  final AccountController account;
  @override
  State<_CommentsSheet> createState() => _CommentsSheetState();
}

class _CommentsSheetState extends State<_CommentsSheet> {
  final _input = TextEditingController();
  final List<ClipComment> _comments = [];
  String _cursor = '';
  String? _error, _requestId;
  bool _loading = true, _sending = false;
  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _input.dispose();
    super.dispose();
  }

  Future<void> _load({bool more = false}) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await widget.account.api.comments(
        widget.clip,
        cursor: more ? _cursor : '',
      );
      if (!mounted) return;
      setState(() {
        if (!more) _comments.clear();
        final existing = _comments.map((row) => row.id).toSet();
        _comments.addAll(
          page.comments.where((row) => !existing.contains(row.id)),
        );
        _cursor = page.nextCursor;
      });
    } catch (e) {
      if (mounted) setState(() => _error = communityMessage(e));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _send() async {
    if (_input.text.trim().isEmpty) return;
    setState(() {
      _sending = true;
      _error = null;
    });
    _requestId ??= CommunityApi.requestId();
    try {
      final c = await widget.account.api.comment(
        widget.clip,
        _input.text,
        _requestId!,
      );
      if (!mounted) return;
      setState(() {
        if (!_comments.any((row) => row.id == c.id)) _comments.insert(0, c);
        _input.clear();
        _requestId = null;
      });
    } catch (e) {
      if (e is CommunityFailure && e.code == 'not_authenticated') {
        widget.account.expired();
      }
      if (mounted) setState(() => _error = communityMessage(e));
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _delete(ClipComment row) async {
    try {
      await widget.account.api.deleteComment(widget.clip, row.id);
      if (mounted) setState(() => _comments.removeWhere((c) => c.id == row.id));
    } catch (e) {
      if (mounted) setState(() => _error = communityMessage(e));
    }
  }

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.only(bottom: MediaQuery.viewInsetsOf(context).bottom),
    child: SizedBox(
      height: MediaQuery.sizeOf(context).height * .75,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(20, 8, 20, 12),
        child: Column(
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    'コメント',
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                ),
                IconButton(
                  tooltip: '閉じる',
                  onPressed: () => Navigator.pop(context),
                  icon: const Icon(Icons.close),
                ),
              ],
            ),
            if (_error != null)
              Padding(
                padding: const EdgeInsets.symmetric(vertical: 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        _error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
                    ),
                    IconButton(
                      tooltip: 'コメントを再読み込み',
                      onPressed: _loading ? null : () => _load(),
                      icon: const Icon(Icons.refresh),
                    ),
                  ],
                ),
              ),
            Expanded(
              child: _loading && _comments.isEmpty
                  ? const Center(child: Text('コメントを読み込んでいます'))
                  : _comments.isEmpty
                  ? const Center(child: Text('最初のコメントをどうぞ'))
                  : ListenableBuilder(
                      listenable: widget.account,
                      builder: (context, _) => ListView.builder(
                        itemCount: _comments.length + (_cursor.isEmpty ? 0 : 1),
                        itemBuilder: (context, index) {
                          if (index == _comments.length) {
                            return TextButton(
                              onPressed: _loading
                                  ? null
                                  : () => _load(more: true),
                              child: Text(_loading ? '読み込んでいます' : 'コメントをもっと見る'),
                            );
                          }
                          final row = _comments[index];
                          return Padding(
                            padding: const EdgeInsets.symmetric(vertical: 10),
                            child: Row(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                const CircleAvatar(
                                  radius: 18,
                                  child: Icon(Icons.person_outline, size: 20),
                                ),
                                const SizedBox(width: 12),
                                Expanded(
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        '${row.author.name} · @${row.author.id}',
                                        style: Theme.of(context)
                                            .textTheme
                                            .labelMedium,
                                      ),
                                      const SizedBox(height: 4),
                                      Text(row.text),
                                    ],
                                  ),
                                ),
                                if (row.author.id == widget.account.user?.id)
                                  IconButton(
                                    tooltip: '自分のコメントを削除',
                                    onPressed: () => _delete(row),
                                    icon: const Icon(
                                      Icons.delete_outline,
                                      size: 20,
                                    ),
                                  ),
                              ],
                            ),
                          );
                        },
                      ),
                    ),
            ),
            const Divider(),
            ListenableBuilder(
              listenable: widget.account,
              builder: (context, _) => widget.account.user == null
                  ? FilledButton.tonal(
                      onPressed: () =>
                          showAccountDialog(context, widget.account),
                      child: const Text('ログインしてコメント'),
                    )
                  : Row(
                      crossAxisAlignment: CrossAxisAlignment.end,
                      children: [
                        Expanded(
                          child: TextField(
                            controller: _input,
                            enabled: !_sending,
                            maxLength: 500,
                            minLines: 1,
                            maxLines: 3,
                            decoration: const InputDecoration(
                              hintText: 'コメントを追加',
                              counterText: '',
                            ),
                            onChanged: (_) => setState(() => _requestId = null),
                          ),
                        ),
                        const SizedBox(width: 8),
                        IconButton.filled(
                          tooltip: 'コメントを投稿',
                          onPressed: _sending || _input.text.trim().isEmpty
                              ? null
                              : _send,
                          icon: Icon(
                            _sending ? Icons.hourglass_top : Icons.send,
                          ),
                        ),
                      ],
                    ),
            ),
          ],
        ),
      ),
    ),
  );
}
