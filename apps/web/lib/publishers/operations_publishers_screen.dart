import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import '../community/account_controller.dart';
import '../community/community_api.dart';
import '../widgets/rangle_header.dart';
import '../widgets/viewer_viewport.dart';
import 'publisher_api.dart';
import 'publisher_deletion_screen.dart';
import 'publisher_forms.dart';
import 'publisher_staff_screen.dart';

class OperationsPublishersScreen extends StatefulWidget {
  const OperationsPublishersScreen({
    required this.account,
    this.api,
    super.key,
  });
  final AccountController account;
  final PublisherApi? api;
  @override
  State<OperationsPublishersScreen> createState() =>
      _OperationsPublishersScreenState();
}

class _OperationsPublishersScreenState
    extends State<OperationsPublishersScreen> {
  late final PublisherApi _api;
  List<PublisherEntry> _entries = [];
  String _cursor = '';
  String? _error;
  bool _loading = true, _busy = false;
  int _epoch = 0;
  @override
  void initState() {
    super.initState();
    _api = widget.api ?? PublisherApi(endpoint: widget.account.api.endpoint);
    widget.account.addListener(_accountChanged);
    unawaited(_load());
  }

  void _accountChanged() {
    if (!mounted || widget.account.user?.isAdmin == true) return;
    _epoch++;
    setState(() {
      _entries = [];
      _cursor = '';
      _loading = false;
      _error = '運営アカウントでログインしてください。';
    });
  }

  @override
  void dispose() {
    _epoch++;
    widget.account.removeListener(_accountChanged);
    if (widget.api == null) _api.close();
    super.dispose();
  }

  Future<void> _load({bool more = false}) async {
    if (widget.account.user?.isAdmin != true) {
      _accountChanged();
      return;
    }
    final epoch = ++_epoch;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await _api.publishers(cursor: more ? _cursor : '');
      if (!mounted || epoch != _epoch) return;
      setState(() {
        _entries = more ? [..._entries, ...page.entries] : page.entries;
        _cursor = page.nextCursor;
      });
    } catch (e) {
      if (mounted && epoch == _epoch) {
        if (e is CommunityFailure && e.code == 'not_authenticated') {
          widget.account.expired();
        }
        setState(() => _error = publisherMessage(e));
      }
    } finally {
      if (mounted && epoch == _epoch) setState(() => _loading = false);
    }
  }

  Future<void> _create() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      if (await provisionPublisher(context, _api) && mounted) await _load();
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _open(PublisherEntry entry) async {
    await Navigator.of(context).push<void>(
      MaterialPageRoute(
        builder: (_) => PublisherOperationsDetail(
          api: _api,
          entry: entry,
          onAuthenticationExpired: widget.account.expired,
        ),
      ),
    );
    if (mounted) await _load();
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: Scaffold(
      appBar: RangleHeader(
        title: '団体管理',
        onBack: _busy ? null : () => Navigator.pop(context),
        actions: [
          IconButton(
            tooltip: '再読み込み',
            onPressed: _loading || _busy ? null : _load,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 600),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: [
                Text('投稿する団体', style: Theme.of(context).textTheme.titleLarge),
                const SizedBox(height: 20),
                if (widget.account.user?.isAdmin == true)
                  FilledButton.icon(
                    onPressed: _busy || _loading ? null : _create,
                    icon: const Icon(Icons.add_business_outlined),
                    label: const Text('団体を登録'),
                  ),
                const SizedBox(height: 16),
                if (_error != null) ...[
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
                    onPressed: _loading ? null : _load,
                    child: const Text('再試行'),
                  ),
                ],
                if (_loading) const LinearProgressIndicator(),
                if (!_loading && _error == null && _entries.isEmpty)
                  const Text('まだ団体は登録されていません。'),
                for (final entry in _entries)
                  Card(
                    child: ListTile(
                      title: Text(entry.account.name),
                      subtitle: Text(
                        '${entry.account.id} · ${entry.statusLabel}${entry.deletionRequested ? ' · 削除依頼あり' : ''}',
                      ),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: _busy ? null : () => _open(entry),
                    ),
                  ),
                if (_cursor.isNotEmpty)
                  OutlinedButton(
                    onPressed: _loading || _busy
                        ? null
                        : () => _load(more: true),
                    child: const Text('さらに表示'),
                  ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

class PublisherOperationsDetail extends StatefulWidget {
  const PublisherOperationsDetail({
    required this.api,
    required this.entry,
    this.onAuthenticationExpired,
    super.key,
  });
  final PublisherApi api;
  final PublisherEntry entry;
  final VoidCallback? onAuthenticationExpired;
  @override
  State<PublisherOperationsDetail> createState() =>
      _PublisherOperationsDetailState();
}

class _PublisherOperationsDetailState extends State<PublisherOperationsDetail> {
  List<PublisherInput> _inputs = [];
  Set<String> _selected = {}, _saved = {};
  bool _loading = true, _busy = false;
  String? _error;
  int _epoch = 0;
  bool _deleting = false;
  @override
  void initState() {
    super.initState();
    if (widget.entry.manageable) {
      unawaited(_load());
    } else {
      _loading = false;
    }
  }

  @override
  void dispose() {
    _epoch++;
    super.dispose();
  }

  void _accept(List<PublisherInput> inputs) {
    _inputs = inputs;
    _saved = {
      for (final input in inputs)
        if (input.enabled) input.id,
    };
    _selected = {..._saved};
  }

  Future<void> _load() async {
    final epoch = ++_epoch;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final inputs = await widget.api.inputs(widget.entry.account.id);
      if (mounted && epoch == _epoch) setState(() => _accept(inputs));
    } catch (e) {
      _failed(e, epoch);
    } finally {
      if (mounted && epoch == _epoch) setState(() => _loading = false);
    }
  }

  void _failed(Object error, int epoch) {
    if (!mounted || epoch != _epoch) return;
    if (error is CommunityFailure && error.code == 'not_authenticated') {
      widget.onAuthenticationExpired?.call();
    }
    setState(() => _error = publisherMessage(error));
  }

  Future<void> _save() async {
    if (_busy || _loading) return;
    final epoch = ++_epoch;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final inputs = await widget.api.assignInputs(
        widget.entry.account.id,
        _selected,
      );
      if (!mounted || epoch != _epoch) return;
      setState(() => _accept(inputs));
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('入力の割当を保存しました。')));
    } catch (e) {
      _failed(e, epoch);
    } finally {
      if (mounted && epoch == _epoch) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: Scaffold(
      appBar: RangleHeader(
        title: '団体の設定',
        onBack: _busy ? null : () => Navigator.pop(context),
      ),
      body: SafeArea(
        child: Center(
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 600),
            child: ListView(
              padding: const EdgeInsets.all(20),
              children: [
                Text(
                  widget.entry.account.name,
                  style: Theme.of(context).textTheme.titleLarge,
                ),
                const SizedBox(height: 8),
                Text(
                  '${widget.entry.account.id} · ${widget.entry.statusLabel}',
                ),
                const SizedBox(height: 24),
                OutlinedButton.icon(
                  onPressed: _busy
                      ? null
                      : () async {
                          final state = await Navigator.of(context)
                              .push<String>(
                                MaterialPageRoute(
                                  builder: (_) => PublisherDeletionScreen(
                                    api: widget.api,
                                    account: widget.entry.account,
                                    operator: true,
                                    onAuthenticationExpired:
                                        widget.onAuthenticationExpired,
                                  ),
                                ),
                              );
                          if (mounted &&
                              (state == 'deleting' || state == 'deleted')) {
                            setState(() {
                              _deleting = true;
                              _epoch++;
                              _inputs = [];
                            });
                          }
                        },
                  icon: const Icon(Icons.delete_outline),
                  label: Text(
                    widget.entry.manageable && !_deleting ? '団体の削除' : '削除の状況',
                  ),
                ),
                if (widget.entry.manageable && !_deleting) ...[
                  const SizedBox(height: 24),
                  OutlinedButton.icon(
                    onPressed: _busy
                        ? null
                        : () => Navigator.of(context).push<void>(
                            MaterialPageRoute(
                              builder: (_) => PublisherStaffScreen(
                                api: widget.api,
                                account: widget.entry.account,
                                operator: true,
                              ),
                            ),
                          ),
                    icon: const Icon(Icons.people_outline),
                    label: const Text('担当者管理'),
                  ),
                  const SizedBox(height: 32),
                  Text(
                    '利用できるデータ',
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  const SizedBox(height: 8),
                  if (_loading) const LinearProgressIndicator(),
                  if (_error != null) ...[
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
                      child: const Text('再読み込み'),
                    ),
                  ],
                  for (final input in _inputs)
                    CheckboxListTile(
                      contentPadding: EdgeInsets.zero,
                      title: Text(input.title),
                      subtitle: Text(input.modeLabel),
                      value: _selected.contains(input.id),
                      onChanged: _busy || _loading
                          ? null
                          : (selected) => setState(() {
                              if (selected == true) {
                                _selected.add(input.id);
                              } else {
                                _selected.remove(input.id);
                              }
                            }),
                    ),
                  const SizedBox(height: 16),
                  FilledButton(
                    onPressed:
                        _loading ||
                            _busy ||
                            _inputs.isEmpty ||
                            setEquals(_saved, _selected)
                        ? null
                        : _save,
                    child: Text(_busy ? '保存中' : '割当を保存'),
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
