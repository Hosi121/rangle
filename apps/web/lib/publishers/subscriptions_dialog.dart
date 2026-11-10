import 'package:flutter/material.dart';

import '../community/account_controller.dart';
import '../community/community_api.dart';
import '../models/publisher_account.dart';
import '../widgets/viewer_modal.dart';
import 'public_publisher_api.dart';

Future<String?> showPublisherSubscriptions(
  BuildContext context,
  AccountController account,
) => showViewerDialog<String>(
  context: context,
  builder: (_) => SubscriptionsDialog(account: account),
);

class SubscriptionsDialog extends StatefulWidget {
  const SubscriptionsDialog({required this.account, super.key});
  final AccountController account;
  @override
  State<SubscriptionsDialog> createState() => _SubscriptionsDialogState();
}

class _SubscriptionsDialogState extends State<SubscriptionsDialog> {
  late final _owner = widget.account.user?.id;
  final _rows = <PublisherAccount>[];
  String _cursor = '';
  String? _error;
  bool _busy = false, _more = true;
  int _epoch = 0;
  bool get _sameAccount => _owner != null && widget.account.user?.id == _owner;
  @override
  void initState() {
    super.initState();
    widget.account.addListener(_accountChanged);
    _load();
  }

  @override
  void dispose() {
    widget.account.removeListener(_accountChanged);
    _epoch++;
    super.dispose();
  }

  void _accountChanged() {
    if (!_sameAccount && mounted) {
      setState(() {
        _epoch++;
        _rows.clear();
        _cursor = '';
        _more = false;
        _busy = false;
        _error = null;
      });
    }
  }

  Future<void> _load() async {
    if (!_sameAccount || _busy || !_more) return;
    final epoch = _epoch;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final page = await widget.account.api.publisherSubscriptions(
        cursor: _cursor,
      );
      if (!mounted || epoch != _epoch || !_sameAccount) return;
      if (page.nextCursor.isNotEmpty && page.nextCursor == _cursor) {
        throw const FormatException('Subscription cursor did not advance.');
      }
      setState(() {
        final ids = _rows.map((row) => row.id).toSet();
        _rows.addAll(page.publishers.where((row) => ids.add(row.id)));
        _cursor = page.nextCursor;
        _more = _cursor.isNotEmpty;
      });
    } catch (e) {
      if (!mounted || epoch != _epoch || !_sameAccount) return;
      if (e is CommunityFailure && e.code == 'not_authenticated') {
        widget.account.expired();
      }
      if (_sameAccount) setState(() => _error = communityMessage(e));
    } finally {
      if (mounted && epoch == _epoch) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Dialog(
    child: ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 400),
      child: SizedBox(
        height: MediaQuery.sizeOf(context).height * .7,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(24, 12, 8, 8),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      'フォロー中の団体',
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
            ),
            Expanded(
              child: ListView(
                children: [
                  if (!_sameAccount)
                    const Padding(
                      padding: EdgeInsets.all(24),
                      child: Text('ログインしてフォロー中の団体を確認してください。'),
                    ),
                  for (final row in _rows)
                    ListTile(
                      leading: const Icon(Icons.groups_outlined),
                      title: Text(row.name),
                      subtitle: Text('@${row.id}'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: _sameAccount
                          ? () => Navigator.pop(context, row.id)
                          : null,
                    ),
                  if (_sameAccount &&
                      _rows.isEmpty &&
                      !_busy &&
                      _error == null &&
                      !_more) ...[
                    const Padding(
                      padding: EdgeInsets.all(24),
                      child: Text('フォロー中の団体はまだありません。'),
                    ),
                    TextButton(
                      onPressed: () => Navigator.pop(context),
                      child: const Text('フィードへ戻る'),
                    ),
                  ],
                ],
              ),
            ),
            if (_error != null)
              Padding(padding: const EdgeInsets.all(16), child: Text(_error!)),
            if (_sameAccount && (_more || _error != null))
              TextButton(
                onPressed: _busy ? null : _load,
                child: Text(
                  _busy
                      ? '読み込んでいます'
                      : _error == null
                      ? 'もっと見る'
                      : '再読み込み',
                ),
              ),
          ],
        ),
      ),
    ),
  );
}
