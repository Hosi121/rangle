import 'package:flutter/material.dart';

import '../widgets/viewer_modal.dart';
import 'account_controller.dart';
import 'community_api.dart';

Future<String?> showCheerHistory(
  BuildContext context,
  AccountController account,
) => showViewerDialog<String>(
  context: context,
  builder: (_) => CheerHistoryDialog(account: account),
);

class CheerHistoryDialog extends StatefulWidget {
  const CheerHistoryDialog({required this.account, super.key});
  final AccountController account;
  @override
  State<CheerHistoryDialog> createState() => _CheerHistoryDialogState();
}

class _CheerHistoryDialogState extends State<CheerHistoryDialog> {
  late final String? _owner = widget.account.user?.id;
  final _rows = <CheeredClip>[];
  String _cursor = '';
  bool _busy = false, _hasMore = true;
  String? _error;
  int _epoch = 0;
  bool get _sameAccount => _owner != null && widget.account.user?.id == _owner;
  @override
  void initState() {
    super.initState();
    widget.account.addListener(_accountChanged);
    _load();
  }

  void _accountChanged() {
    if (!_sameAccount && mounted) {
      setState(() {
        _epoch++;
        _rows.clear();
        _cursor = '';
        _hasMore = false;
        _busy = false;
        _error = null;
      });
    }
  }

  @override
  void dispose() {
    widget.account.removeListener(_accountChanged);
    _epoch++;
    super.dispose();
  }

  Future<void> _load() async {
    if (_busy || !_sameAccount || !_hasMore) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    final epoch = _epoch;
    try {
      final page = await widget.account.api.cheeredClips(cursor: _cursor);
      if (!mounted || epoch != _epoch || !_sameAccount) return;
      if (page.nextCursor.isNotEmpty && page.nextCursor == _cursor) {
        throw const FormatException('History cursor did not advance.');
      }
      setState(() {
        final ids = _rows.map((row) => row.id).toSet();
        _rows.addAll(page.clips.where((row) => ids.add(row.id)));
        _cursor = page.nextCursor;
        _hasMore = _cursor.isNotEmpty;
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
                      '声援を送った動画',
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
            if (!_sameAccount)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Text('ログインして履歴を確認してください。'),
              ),
            Expanded(
              child: ListView.builder(
                itemCount: _rows.length,
                itemBuilder: (context, i) {
                  final row = _rows[i], at = row.lastCheeredAt.toLocal();
                  return ListTile(
                    leading: const Icon(Icons.celebration_outlined),
                    title: Text(row.deleted ? '削除済み動画' : row.title),
                    subtitle: Text(
                      '${row.sport} · ${at.year}/${at.month}/${at.day}',
                    ),
                    trailing: row.deleted
                        ? null
                        : const Icon(Icons.chevron_right),
                    enabled: !row.deleted,
                    onTap: _sameAccount && !row.deleted
                        ? () => Navigator.pop(context, row.id)
                        : null,
                  );
                },
              ),
            ),
            if (_sameAccount && _rows.isEmpty && !_busy && _error == null)
              const Padding(
                padding: EdgeInsets.all(24),
                child: Text('声援を送った動画がここに並びます。'),
              ),
            if (_error != null)
              Padding(padding: const EdgeInsets.all(16), child: Text(_error!)),
            if (_sameAccount && (_hasMore || _error != null))
              Padding(
                padding: const EdgeInsets.all(12),
                child: TextButton(
                  onPressed: _busy ? null : _load,
                  child: Text(
                    _busy
                        ? '履歴を読み込んでいます'
                        : _error != null
                        ? '再読み込み'
                        : 'もっと見る',
                  ),
                ),
              ),
          ],
        ),
      ),
    ),
  );
}
