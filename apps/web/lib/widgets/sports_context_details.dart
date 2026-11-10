import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/sports_context.dart';

class SportsContextDetails extends StatelessWidget {
  const SportsContextDetails({required this.context, this.onSearch, super.key});
  final SportsContext context;
  final ValueChanged<String>? onSearch;
  @override
  Widget build(BuildContext context) {
    Widget row(String label, String name) {
      final searchable =
          onSearch != null &&
          utf8.encode(name).length <= 256 &&
          name.trim().split(RegExp(r'\s+')).length <= 8;
      return ListTile(
        contentPadding: EdgeInsets.zero,
        title: Text(name),
        subtitle: Text(label),
        trailing: searchable ? const Icon(Icons.search, size: 20) : null,
        onTap: searchable ? () => onSearch!(name) : null,
      );
    }

    final event = this.context.event;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (this.context.publisher case final publisher?)
          row('投稿者', publisher.name),
        if (event != null) ...[
          row(switch (event.kind) {
            'match' => '試合',
            'training' => '練習',
            _ => 'デモ',
          }, event.entity.name),
          if (event.competition case final competition?)
            row('大会', competition.name),
          if (event.venue case final venue?)
            row(event.city.isEmpty ? '会場' : '会場・${event.city}', venue.name),
          if (event.startsAt case final at?)
            Text(
              '開催日 ${at.toLocal().year}/${at.toLocal().month}/${at.toLocal().day}',
            ),
          for (final team in event.teams) row('チーム', team.name),
        ],
        for (final athlete in this.context.athletes) row('選手', athlete.name),
      ],
    );
  }
}
