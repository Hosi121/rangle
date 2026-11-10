import 'dart:async';

import 'package:flutter/material.dart';

import '../generation/generation_screen.dart';
import '../generation/generation_api.dart';
import '../live/live_api.dart';
import '../viewer/viewer_adapter.dart';
import '../widgets/rangle_header.dart';
import '../widgets/viewer_viewport.dart';
import 'publisher_api.dart';
import 'publisher_controller.dart';
import 'publisher_deletion_screen.dart';
import 'publisher_forms.dart';
import 'publisher_staff_screen.dart';

class PublisherHome extends StatefulWidget {
  const PublisherHome({
    required this.api,
    required this.onOpenResult,
    this.onPublicationChanged,
    this.generation,
    this.live,
    this.liveFactory,
    super.key,
  });
  final PublisherApi api;
  final GenerationApi? generation;
  final LiveApi? live;
  final ViewerFactory? liveFactory;
  final ValueChanged<Uri> onOpenResult;
  final VoidCallback? onPublicationChanged;
  @override
  State<PublisherHome> createState() => _PublisherHomeState();
}

class _PublisherHomeState extends State<PublisherHome> {
  late final PublisherController _session;
  bool _busy = false;
  @override
  void initState() {
    super.initState();
    _session = PublisherController(widget.api);
    unawaited(_session.initialize());
  }

  @override
  void dispose() {
    _session.dispose();
    super.dispose();
  }

  Future<void> _menu(String action) async {
    final identity = _session.identity;
    if (_busy || identity == null) return;
    setState(() => _busy = true);
    try {
      switch (action) {
        case 'staff':
          await Navigator.of(context).push<void>(
            MaterialPageRoute(
              builder: (_) => PublisherStaffScreen(
                api: widget.api,
                account: identity.account,
                currentStaff: identity.staff.id,
                onSelfChanged: _session.initialize,
              ),
            ),
          );
        case 'password':
          if (await changePublisherPassword(context, widget.api)) {
            _session.expired();
          }
        case 'deletion':
          await Navigator.of(context).push<void>(
            MaterialPageRoute(
              builder: (_) => PublisherDeletionScreen(
                api: widget.api,
                account: identity.account,
                onAuthenticationExpired: _session.expired,
              ),
            ),
          );
        case 'logout':
          await _session.logout();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(publisherMessage(e))));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => ViewerViewport(
    child: ListenableBuilder(
      listenable: _session,
      builder: (context, _) {
        final identity = _session.identity;
        if (_session.loading) {
          return Scaffold(
            appBar: RangleHeader(
              title: '団体管理',
              onBack: () => Navigator.pop(context),
            ),
            body: const Center(child: CircularProgressIndicator()),
          );
        }
        if (identity == null) return _PublisherLogin(session: _session);
        return GenerationScreen(
          key: ValueKey(
            'publisher:${identity.account.id}:${identity.staff.id}',
          ),
          account: null,
          api: widget.generation,
          live: widget.live,
          liveFactory: widget.liveFactory,
          publisher: _session,
          onBack: () => Navigator.pop(context),
          onOpenResult: widget.onOpenResult,
          onPublicationChanged: widget.onPublicationChanged,
          headerActions: [
            PopupMenuButton<String>(
              tooltip: '団体アカウント',
              enabled: !_busy,
              icon: const Icon(Icons.manage_accounts_outlined),
              onSelected: _menu,
              itemBuilder: (_) => [
                if (identity.canManageStaff)
                  const PopupMenuItem(value: 'staff', child: Text('担当者管理')),
                const PopupMenuItem(value: 'password', child: Text('パスワード変更')),
                if (identity.canManageStaff)
                  const PopupMenuItem(value: 'deletion', child: Text('団体の削除')),
                const PopupMenuItem(value: 'logout', child: Text('ログアウト')),
              ],
            ),
          ],
        );
      },
    ),
  );
}

class _PublisherLogin extends StatefulWidget {
  const _PublisherLogin({required this.session});
  final PublisherController session;
  @override
  State<_PublisherLogin> createState() => _PublisherLoginState();
}

class _PublisherLoginState extends State<_PublisherLogin> {
  final _form = GlobalKey<FormState>();
  final _account = TextEditingController(),
      _username = TextEditingController(),
      _password = TextEditingController();
  bool _busy = false;
  String? _error;
  @override
  void dispose() {
    _account.dispose();
    _username.dispose();
    _password.dispose();
    super.dispose();
  }

  Future<void> _login() async {
    if (_busy || !_form.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.session.login(
        _account.text.trim().toLowerCase(),
        _username.text.trim(),
        _password.text,
      );
    } catch (e) {
      if (mounted) setState(() => _error = publisherMessage(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: RangleHeader(title: '団体ログイン', onBack: () => Navigator.pop(context)),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 440),
          child: ListView(
            shrinkWrap: true,
            padding: const EdgeInsets.all(24),
            children: [
              Text(
                '団体担当者のログイン',
                style: Theme.of(context).textTheme.headlineSmall,
              ),
              const SizedBox(height: 24),
              if (!widget.session.available) ...[
                Text(widget.session.error ?? '現在ログインを利用できません。'),
                const SizedBox(height: 16),
                OutlinedButton(
                  onPressed: widget.session.initialize,
                  child: const Text('再読み込み'),
                ),
              ] else
                Form(
                  key: _form,
                  child: AutofillGroup(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        TextFormField(
                          key: const Key('publisher-login-account'),
                          controller: _account,
                          enabled: !_busy,
                          decoration: const InputDecoration(labelText: '団体ID'),
                          validator: publisherIdError,
                          textInputAction: TextInputAction.next,
                        ),
                        const SizedBox(height: 16),
                        TextFormField(
                          key: const Key('publisher-login-username'),
                          controller: _username,
                          enabled: !_busy,
                          decoration: const InputDecoration(labelText: '担当者名'),
                          validator: staffNameError,
                          autofillHints: const [AutofillHints.username],
                          textInputAction: TextInputAction.next,
                        ),
                        const SizedBox(height: 16),
                        TextFormField(
                          key: const Key('publisher-login-password'),
                          controller: _password,
                          enabled: !_busy,
                          decoration: const InputDecoration(labelText: 'パスワード'),
                          obscureText: true,
                          autofillHints: const [AutofillHints.password],
                          validator: (value) => value?.isNotEmpty == true
                              ? null
                              : 'パスワードを入力してください。',
                          onFieldSubmitted: (_) => _login(),
                        ),
                        const SizedBox(height: 24),
                        FilledButton(
                          onPressed: _busy ? null : _login,
                          child: Text(_busy ? 'ログイン中' : 'ログイン'),
                        ),
                        if (_error != null)
                          Padding(
                            padding: const EdgeInsets.only(top: 12),
                            child: Semantics(
                              liveRegion: true,
                              child: Text(
                                _error!,
                                style: TextStyle(
                                  color: Theme.of(context).colorScheme.error,
                                ),
                              ),
                            ),
                          ),
                        const SizedBox(height: 16),
                        const Text('パスワードを忘れた場合は、団体管理者またはrangle運営へご連絡ください。'),
                      ],
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
    ),
  );
}
