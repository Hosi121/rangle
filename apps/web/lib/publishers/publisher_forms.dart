import 'package:flutter/material.dart';

import '../widgets/viewer_modal.dart';
import 'publisher_api.dart';

String? publisherIdError(String? value) =>
    RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$')
        .hasMatch(value?.trim().toLowerCase() ?? '')
    ? null
    : '小文字の英数字・_・-で1〜64文字にしてください。';
String? staffNameError(String? value) =>
    RegExp(r'^[a-z0-9_-]{3,32}$').hasMatch(value?.trim().toLowerCase() ?? '')
    ? null
    : '英数字・_・-で3〜32文字にしてください。';
String? staffPasswordError(String? value) =>
    value != null && value.runes.length >= 15 && value.runes.length <= 128
    ? null
    : '15〜128文字で入力してください。';

Future<bool> showPublisherForm(
  BuildContext context, {
  required String title,
  required Widget Function(bool busy, VoidCallback refresh) fields,
  required Future<void> Function() submit,
  String submitLabel = '保存',
  VoidCallback? onDispose,
}) async =>
    await showViewerDialog<bool>(
      context: context,
      builder: (_) => _PublisherForm(
        title: title,
        fields: fields,
        submit: submit,
        submitLabel: submitLabel,
        onDispose: onDispose,
      ),
    ) ??
    false;

class _PublisherForm extends StatefulWidget {
  const _PublisherForm({
    required this.title,
    required this.fields,
    required this.submit,
    required this.submitLabel,
    this.onDispose,
  });
  final String title, submitLabel;
  final VoidCallback? onDispose;
  final Widget Function(bool, VoidCallback) fields;
  final Future<void> Function() submit;
  @override
  State<_PublisherForm> createState() => _PublisherFormState();
}

class _PublisherFormState extends State<_PublisherForm> {
  final _form = GlobalKey<FormState>();
  bool _busy = false;
  String? _error;
  @override
  void dispose() {
    widget.onDispose?.call();
    super.dispose();
  }

  Future<void> _save() async {
    if (_busy || !_form.currentState!.validate()) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.submit();
      if (mounted) Navigator.pop(context, true);
    } catch (e) {
      if (mounted) setState(() => _error = publisherMessage(e));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !_busy,
    child: Dialog(
      child: SizedBox(
        width: 400,
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: Form(
            key: _form,
            child: AutofillGroup(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    widget.title,
                    style: Theme.of(context).textTheme.titleLarge,
                  ),
                  const SizedBox(height: 24),
                  widget.fields(_busy, () => setState(() {})),
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
                  const SizedBox(height: 24),
                  Row(
                    children: [
                      Expanded(
                        child: OutlinedButton(
                          onPressed: _busy
                              ? null
                              : () => Navigator.pop(context, false),
                          child: const Text('キャンセル'),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: FilledButton(
                          onPressed: _busy ? null : _save,
                          child: Text(_busy ? '保存中' : widget.submitLabel),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    ),
  );
}

Widget _roleField(String role, bool busy, ValueChanged<String> changed) =>
    DropdownButtonFormField<String>(
      initialValue: role,
      decoration: const InputDecoration(labelText: '役割'),
      items: const [
        DropdownMenuItem(value: 'editor', child: Text('投稿担当')),
        DropdownMenuItem(value: 'administrator', child: Text('団体管理者')),
      ],
      onChanged: busy
          ? null
          : (value) {
              if (value != null) changed(value);
            },
    );
List<Widget> _passwordFields(
  TextEditingController password,
  TextEditingController confirm,
  bool busy,
) => [
  TextFormField(
    key: const Key('publisher-new-password'),
    controller: password,
    enabled: !busy,
    decoration: const InputDecoration(
      labelText: '新しいパスワード',
      helperText: '15〜128文字',
    ),
    obscureText: true,
    autofillHints: const [AutofillHints.newPassword],
    validator: staffPasswordError,
  ),
  const SizedBox(height: 16),
  TextFormField(
    key: const Key('publisher-confirm-password'),
    controller: confirm,
    enabled: !busy,
    decoration: const InputDecoration(labelText: 'パスワードを再入力'),
    obscureText: true,
    autofillHints: const [AutofillHints.newPassword],
    validator: (value) => value == password.text ? null : 'パスワードが一致しません。',
  ),
];

Future<bool> createPublisherStaff(
  BuildContext context,
  PublisherApi api,
  String account, {
  bool operator = false,
}) async {
  final name = TextEditingController(),
      password = TextEditingController(),
      confirm = TextEditingController();
  var role = 'editor';
  return await showPublisherForm(
    context,
    onDispose: () {
      name.dispose();
      password.dispose();
      confirm.dispose();
    },
    title: '担当者を追加',
    submitLabel: '追加',
    fields: (busy, refresh) => Column(
      children: [
        TextFormField(
          controller: name,
          enabled: !busy,
          decoration: const InputDecoration(labelText: '担当者名'),
          validator: staffNameError,
          autofillHints: const [AutofillHints.username],
        ),
        const SizedBox(height: 16),
        _roleField(role, busy, (value) {
          role = value;
          refresh();
        }),
        const SizedBox(height: 16),
        ..._passwordFields(password, confirm, busy),
      ],
    ),
    submit: () => api.createStaff(
      account,
      name.text.trim(),
      password.text,
      role,
      operator: operator,
    ),
  );
}

Future<bool> editPublisherStaff(
  BuildContext context,
  PublisherApi api,
  String account,
  PublisherStaff staff, {
  bool operator = false,
}) async {
  var role = staff.role, disabled = staff.disabled;
  return showPublisherForm(
    context,
    title: staff.username,
    fields: (busy, refresh) => Column(
      children: [
        _roleField(role, busy, (value) {
          role = value;
          refresh();
        }),
        const SizedBox(height: 16),
        SwitchListTile(
          contentPadding: EdgeInsets.zero,
          title: const Text('利用を停止'),
          value: disabled,
          onChanged: busy
              ? null
              : (value) {
                  disabled = value;
                  refresh();
                },
        ),
        const Text('変更後は、担当者の再ログインが必要です。'),
      ],
    ),
    submit: () =>
        api.updateStaff(account, staff.id, role, disabled, operator: operator),
  );
}

Future<bool> resetPublisherStaffPassword(
  BuildContext context,
  PublisherApi api,
  String account,
  PublisherStaff staff, {
  bool operator = false,
}) async {
  final password = TextEditingController(), confirm = TextEditingController();
  return await showPublisherForm(
    context,
    onDispose: () {
      password.dispose();
      confirm.dispose();
    },
    title: '${staff.username}のパスワード',
    fields: (busy, _) => Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        ..._passwordFields(password, confirm, busy),
        const SizedBox(height: 12),
        const Text('変更前のログインはすべて失効します。'),
      ],
    ),
    submit: () =>
        api.resetPassword(account, staff.id, password.text, operator: operator),
  );
}

Future<bool> changePublisherPassword(
  BuildContext context,
  PublisherApi api,
) async {
  final current = TextEditingController(),
      password = TextEditingController(),
      confirm = TextEditingController();
  return await showPublisherForm(
    context,
    onDispose: () {
      current.dispose();
      password.dispose();
      confirm.dispose();
    },
    title: 'パスワードを変更',
    fields: (busy, _) => Column(
      children: [
        TextFormField(
          key: const Key('publisher-current-password'),
          controller: current,
          enabled: !busy,
          obscureText: true,
          decoration: const InputDecoration(labelText: '現在のパスワード'),
          autofillHints: const [AutofillHints.password],
          validator: (value) =>
              value?.isNotEmpty == true ? null : '現在のパスワードを入力してください。',
        ),
        const SizedBox(height: 16),
        ..._passwordFields(password, confirm, busy),
        const SizedBox(height: 12),
        const Text('変更後はログインし直してください。'),
      ],
    ),
    submit: () => api.changePassword(current.text, password.text),
  );
}

Future<bool> provisionPublisher(BuildContext context, PublisherApi api) async {
  final id = TextEditingController(),
      name = TextEditingController(),
      username = TextEditingController(),
      password = TextEditingController(),
      confirm = TextEditingController();
  var kind = 'organization';
  return await showPublisherForm(
    context,
    onDispose: () {
      id.dispose();
      name.dispose();
      username.dispose();
      password.dispose();
      confirm.dispose();
    },
    title: '団体を登録',
    submitLabel: '登録',
    fields: (busy, refresh) => Column(
      children: [
        TextFormField(
          key: const Key('publisher-create-id'),
          controller: id,
          enabled: !busy,
          decoration: const InputDecoration(labelText: '団体ID'),
          validator: publisherIdError,
        ),
        const SizedBox(height: 16),
        TextFormField(
          key: const Key('publisher-create-name'),
          controller: name,
          enabled: !busy,
          decoration: const InputDecoration(labelText: '団体名'),
          validator: (value) =>
              value != null &&
                  value.trim().isNotEmpty &&
                  value.runes.length <= 120
              ? null
              : '団体名を120文字以内で入力してください。',
        ),
        const SizedBox(height: 16),
        DropdownButtonFormField<String>(
          initialValue: kind,
          decoration: const InputDecoration(labelText: '種別'),
          items: const [
            DropdownMenuItem(value: 'organization', child: Text('団体')),
            DropdownMenuItem(value: 'company', child: Text('企業')),
          ],
          onChanged: busy
              ? null
              : (value) {
                  if (value != null) {
                    kind = value;
                    refresh();
                  }
                },
        ),
        const SizedBox(height: 16),
        TextFormField(
          key: const Key('publisher-create-username'),
          controller: username,
          enabled: !busy,
          decoration: const InputDecoration(labelText: '最初の管理者の担当者名'),
          validator: staffNameError,
          autofillHints: const [AutofillHints.username],
        ),
        const SizedBox(height: 16),
        ..._passwordFields(password, confirm, busy),
      ],
    ),
    submit: () async {
      await api.provision(
        id: id.text.trim().toLowerCase(),
        name: name.text.trim(),
        kind: kind,
        username: username.text.trim(),
        password: password.text,
      );
    },
  );
}
