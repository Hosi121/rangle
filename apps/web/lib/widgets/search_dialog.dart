import 'dart:convert';

import 'package:flutter/material.dart';

import 'viewer_modal.dart';

Future<String?> showSearchDialog(
  BuildContext context, {
  required String initial,
  required Iterable<String> tags,
}) => showViewerDialog<String>(
  context: context,
  builder: (_) => _SearchDialog(initial: initial, tags: tags.take(12).toList()),
);

class _SearchDialog extends StatefulWidget {
  const _SearchDialog({required this.initial, required this.tags});
  final String initial;
  final List<String> tags;
  @override
  State<_SearchDialog> createState() => _SearchDialogState();
}

class _SearchDialogState extends State<_SearchDialog> {
  late final _input = TextEditingController(text: widget.initial);
  final _form = GlobalKey<FormState>();
  @override
  void dispose() {
    _input.dispose();
    super.dispose();
  }

  void _submit() {
    if (_form.currentState!.validate()) {
      Navigator.pop(context, _input.text.trim());
    }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('ハイライトを検索'),
    content: SingleChildScrollView(
      child: Form(
        key: _form,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextFormField(
              controller: _input,
              autofocus: true,
              decoration: const InputDecoration(
                labelText: '選手・チーム・会場・ハッシュタグ',
                hintText: '試合名、団体名などでも検索',
              ),
              textInputAction: TextInputAction.search,
              onFieldSubmitted: (_) => _submit(),
              validator: (value) => utf8.encode(value ?? '').length > 256
                  ? '256バイト以内で入力してください。'
                  : null,
            ),
            if (widget.tags.isNotEmpty) ...[
              const SizedBox(height: 20),
              const Text('ハッシュタグ'),
              Wrap(
                spacing: 8,
                children: widget.tags
                    .map(
                      (tag) => TextButton(
                        onPressed: () => Navigator.pop(context, '#$tag'),
                        child: Text('#$tag'),
                      ),
                    )
                    .toList(),
              ),
            ],
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('キャンセル'),
      ),
      FilledButton(onPressed: _submit, child: const Text('検索')),
    ],
  );
}
