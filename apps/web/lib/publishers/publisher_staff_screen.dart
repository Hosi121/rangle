import 'dart:async';

import 'package:flutter/material.dart';

import '../community/community_api.dart';
import '../models/publisher_account.dart';
import '../widgets/rangle_header.dart';
import '../widgets/viewer_viewport.dart';
import 'publisher_api.dart';
import 'publisher_forms.dart';

class PublisherStaffScreen extends StatefulWidget {
  const PublisherStaffScreen({
    required this.api,
    required this.account,
    this.operator = false,
    this.currentStaff,
    this.onSelfChanged,
    super.key,
  });
  final PublisherApi api;
  final PublisherAccount account;
  final bool operator;
  final String? currentStaff;
  final Future<void> Function()? onSelfChanged;
  @override
  State<PublisherStaffScreen> createState() => _PublisherStaffScreenState();
}

class _PublisherStaffScreenState extends State<PublisherStaffScreen> {
  List<PublisherStaff> _staff = [];
  String _cursor = '';
  String? _error;
  bool _loading = true, _busy = false;
  int _epoch = 0;
  @override
  void initState() {
    super.initState();
    unawaited(_load());
  }

  @override
  void dispose() {
    _epoch++;
    super.dispose();
  }

  Future<void> _load({bool more = false}) async {
    final epoch = ++_epoch;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final page = await widget.api.staff(
        widget.account.id,
        operator: widget.operator,
        cursor: more ? _cursor : '',
      );
      if (!mounted || epoch != _epoch) return;
      setState(() {
        _staff = more ? [..._staff, ...page.staff] : page.staff;
        _cursor = page.nextCursor;
      });
    } catch (e) {
      if (mounted && epoch == _epoch) {
        setState(() {
          if (e is CommunityFailure && e.code == 'not_authenticated') {
            _staff = [];
            _cursor = '';
          }
          _error = publisherMessage(e);
        });
      }
    } finally {
      if (mounted && epoch == _epoch) setState(() => _loading = false);
    }
  }

  Future<void> _edit(PublisherStaff staff, String action) async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final changed = action == 'password'
          ? await resetPublisherStaffPassword(
              context,
              widget.api,
              widget.account.id,
              staff,
              operator: widget.operator,
            )
          : await editPublisherStaff(
              context,
              widget.api,
              widget.account.id,
              staff,
              operator: widget.operator,
            );
      if (!mounted || !changed) return;
      if (!widget.operator && staff.id == widget.currentStaff) {
        await widget.onSelfChanged?.call();
        if (mounted) Navigator.pop(context);
        return;
      }
      await _load();
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _create() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      if (await createPublisherStaff(
            context,
            widget.api,
            widget.account.id,
            operator: widget.operator,
          ) &&
          mounted) {
        await _load();
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: Scaffold(
      appBar: RangleHeader(
        title: '担当者管理',
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
                Text(
                  widget.account.name,
                  style: Theme.of(context).textTheme.titleLarge,
                ),
                const SizedBox(height: 20),
                FilledButton.icon(
                  onPressed: _busy || _loading ? null : _create,
                  icon: const Icon(Icons.person_add_outlined),
                  label: const Text('担当者を追加'),
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
                for (final staff in _staff)
                  Card(
                    child: ListTile(
                      title: Text(staff.username),
                      subtitle: Text(
                        '${staff.roleLabel}${staff.disabled ? ' · 利用停止中' : ''}',
                      ),
                      trailing: PopupMenuButton<String>(
                        tooltip: '${staff.username}を管理',
                        enabled: !_busy && !_loading,
                        onSelected: (action) => _edit(staff, action),
                        itemBuilder: (_) => const [
                          PopupMenuItem(value: 'role', child: Text('役割・利用状態')),
                          PopupMenuItem(
                            value: 'password',
                            child: Text('パスワード再設定'),
                          ),
                        ],
                      ),
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
