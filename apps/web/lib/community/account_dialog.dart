import 'package:flutter/material.dart';

import '../widgets/viewer_modal.dart';

import 'account_controller.dart';
import 'community_api.dart';

Future<bool> showAccountDialog(
  BuildContext context,
  AccountController? account,
) async {
  if (account == null) {
    ScaffoldMessenger.of(context)
        .showSnackBar(const SnackBar(content: Text('現在ログインを利用できません。')));
    return false;
  }
  await showViewerDialog<void>(
    context: context,
    builder: (_) => _AccountDialog(account: account),
  );
  return account.user != null;
}

class _AccountDialog extends StatefulWidget {
  const _AccountDialog({required this.account});
  final AccountController account;
  @override
  State<_AccountDialog> createState() => _AccountDialogState();
}

class _AccountDialogState extends State<_AccountDialog> {
  final _form = GlobalKey<FormState>();
  final _id = TextEditingController(),
      _name = TextEditingController(),
      _password = TextEditingController(),
      _confirm = TextEditingController();
  bool _register = false, _busy = false, _visible = false;
  String? _error;
  @override
  void dispose() {
    _id.dispose();
    _name.dispose();
    _password.dispose();
    _confirm.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!_form.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.account.authenticate(
        _id.text,
        _name.text,
        _password.text,
        register: _register,
      );
      if (mounted) Navigator.pop(context);
    } catch (e) {
      if (mounted) setState(() => _error = communityMessage(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => ListenableBuilder(
    listenable: widget.account,
    builder: (context, _) {
      final user = widget.account.user;
      return Dialog(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 400),
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Text(
                        user != null
                            ? 'アカウント'
                            : _register
                            ? 'アカウントを作成'
                            : 'ログイン',
                        style: Theme.of(context).textTheme.titleLarge,
                      ),
                    ),
                    IconButton(
                      tooltip: '閉じる',
                      onPressed: _busy ? null : () => Navigator.pop(context),
                      icon: const Icon(Icons.close),
                    ),
                  ],
                ),
                const SizedBox(height: 16),
                if (user != null) ...[
                  Text(
                    user.name,
                    style: Theme.of(context).textTheme.titleMedium,
                  ),
                  Text('@${user.id}'),
                  const SizedBox(height: 24),
                  OutlinedButton(
                    onPressed: _busy
                        ? null
                        : () async {
                            setState(() => _busy = true);
                            try {
                              await widget.account.logout();
                              if (context.mounted) Navigator.pop(context);
                            } catch (e) {
                              if (mounted) {
                                setState(() => _error = communityMessage(e));
                              }
                            } finally {
                              if (mounted) setState(() => _busy = false);
                            }
                          },
                    child: const Text('ログアウト'),
                  ),
                ] else if (widget.account.loading) ...[
                  const Text('アカウントを確認しています'),
                ] else if (!widget.account.available) ...[
                  const Text('現在ログインを利用できません。'),
                  const SizedBox(height: 12),
                  OutlinedButton(
                    onPressed: widget.account.initialize,
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
                            key: const Key('account-username'),
                            controller: _id,
                            autocorrect: false,
                            enabled: !_busy,
                            decoration: const InputDecoration(
                              labelText: 'ユーザーID',
                              helperText: '半角英数字・_・-、3〜32文字',
                            ),
                            autofillHints: const [AutofillHints.username],
                            textInputAction: TextInputAction.next,
                            validator: (value) =>
                                RegExp(r'^[a-zA-Z0-9_-]{3,32}$')
                                    .hasMatch(value?.trim() ?? '')
                                ? null
                                : 'ユーザーIDの入力条件を確認してください。',
                          ),
                          if (_register) ...[
                            const SizedBox(height: 12),
                            TextFormField(
                              key: const Key('account-name'),
                              controller: _name,
                              enabled: !_busy,
                              decoration: const InputDecoration(
                                labelText: '表示名（任意）',
                              ),
                              maxLength: 40,
                              textInputAction: TextInputAction.next,
                            ),
                          ],
                          const SizedBox(height: 12),
                          TextFormField(
                            key: const Key('account-password'),
                            controller: _password,
                            autocorrect: false,
                            enableSuggestions: false,
                            enabled: !_busy,
                            obscureText: !_visible,
                            autofillHints: [
                              _register
                                  ? AutofillHints.newPassword
                                  : AutofillHints.password,
                            ],
                            decoration: InputDecoration(
                              labelText: 'パスワード',
                              helperText: _register ? '15〜128文字' : null,
                              suffixIcon: IconButton(
                                tooltip: _visible ? 'パスワードを隠す' : 'パスワードを表示',
                                onPressed: () =>
                                    setState(() => _visible = !_visible),
                                icon: Icon(
                                  _visible
                                      ? Icons.visibility_off
                                      : Icons.visibility,
                                ),
                              ),
                            ),
                            validator: (value) {
                              if (value == null || value.isEmpty) {
                                return 'パスワードを入力してください。';
                              }
                              if (_register &&
                                  (value.runes.length < 15 ||
                                      value.runes.length > 128)) {
                                return '15〜128文字で入力してください。';
                              }
                              return null;
                            },
                            onFieldSubmitted: _busy ? null : (_) => _submit(),
                          ),
                          if (_register) ...[
                            const SizedBox(height: 12),
                            TextFormField(
                              key: const Key('account-confirm-password'),
                              controller: _confirm,
                              autocorrect: false,
                              enableSuggestions: false,
                              enabled: !_busy,
                              obscureText: true,
                              decoration: const InputDecoration(
                                labelText: 'パスワード（確認）',
                              ),
                              autofillHints: const [AutofillHints.newPassword],
                              validator: (value) => value == _password.text
                                  ? null
                                  : 'パスワードが一致しません。',
                              onFieldSubmitted: _busy ? null : (_) => _submit(),
                            ),
                          ],
                          const SizedBox(height: 24),
                          FilledButton(
                            onPressed: _busy ? null : _submit,
                            child: Text(
                              _busy
                                  ? '処理しています'
                                  : _register
                                  ? '登録する'
                                  : 'ログインする',
                            ),
                          ),
                          TextButton(
                            onPressed: _busy
                                ? null
                                : () => setState(() {
                                    _register = !_register;
                                    _error = null;
                                  }),
                            child: Text(_register ? 'ログインへ戻る' : 'アカウントを作成'),
                          ),
                        ],
                      ),
                    ),
                  ),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Text(
                    _error!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ],
              ],
            ),
          ),
        ),
      );
    },
  );
}
