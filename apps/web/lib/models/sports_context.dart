import 'publisher_account.dart';

/// Reviewed publication metadata; it never grants publishing privileges.
class SportsEntity {
  const SportsEntity(this.id, this.name);
  final String id, name;
  factory SportsEntity.fromJson(Map<String, dynamic> data) {
    final id = data['id'], name = data['name'];
    if (id is! String ||
        !RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(id) ||
        name is! String ||
        name.trim().isEmpty ||
        name.runes.length > 120 ||
        RegExp(r'[\x00-\x1f\x7f-\x9f]').hasMatch(name)) {
      throw const FormatException('Invalid sporting entity.');
    }
    return SportsEntity(id, name);
  }
}

class SportsEvent {
  const SportsEvent({
    required this.entity,
    required this.kind,
    this.competition,
    this.startsAt,
    this.venue,
    this.city = '',
    this.teams = const [],
  });
  final SportsEntity entity;
  final String kind, city;
  final DateTime? startsAt;
  final SportsEntity? competition, venue;
  final List<SportsEntity> teams;
  factory SportsEvent.fromJson(Map<String, dynamic> data) {
    final kind = data['kind'];
    if (!{'match', 'training', 'demonstration'}.contains(kind)) {
      throw const FormatException('Invalid event kind.');
    }
    final venueData = _object(data['venue']);
    final city = venueData?['city'] ?? '';
    if (city is! String ||
        city.runes.length > 120 ||
        city.isNotEmpty &&
            (city.trim().isEmpty ||
                RegExp(r'[\x00-\x1f\x7f-\x9f]').hasMatch(city))) {
      throw const FormatException('Invalid venue city.');
    }
    final at = data['starts_at'];
    final date = at == null
        ? null
        : at is String
        ? DateTime.tryParse(at)
        : null;
    if (at != null && (date == null || date.year < 1900 || date.year > 2200)) {
      throw const FormatException('Invalid event date.');
    }
    final competition = _object(data['competition']);
    return SportsEvent(
      entity: SportsEntity.fromJson(data),
      kind: kind as String,
      competition: competition == null
          ? null
          : SportsEntity.fromJson(competition),
      startsAt: date,
      venue: venueData == null ? null : SportsEntity.fromJson(venueData),
      city: city,
      teams: _entities(data['teams'], 16),
    );
  }
}

class SportsContext {
  const SportsContext({this.publisher, this.event, this.athletes = const []});
  final PublisherAccount? publisher;
  final SportsEvent? event;
  final List<SportsEntity> athletes;
  factory SportsContext.fromJson(Map<String, dynamic> data) {
    final publisher = _object(data['publisher']),
        event = _object(data['event']);
    final athletes = _entities(data['athletes'], 64);
    if (publisher == null && event == null && athletes.isEmpty) {
      throw const FormatException('Empty sporting context.');
    }
    return SportsContext(
      publisher: publisher == null
          ? null
          : PublisherAccount.fromJson(publisher),
      event: event == null ? null : SportsEvent.fromJson(event),
      athletes: athletes,
    );
  }
}

Map<String, dynamic>? _object(Object? value) {
  if (value == null) return null;
  if (value is! Map<String, dynamic>) {
    throw const FormatException('Invalid sporting context.');
  }
  return value;
}

List<SportsEntity> _entities(Object? data, int max) {
  if (data == null) return const [];
  if (data is! List || data.length > max) {
    throw const FormatException('Invalid sporting entities.');
  }
  final ids = <String>{};
  return List.unmodifiable(
    data.map((row) {
      final entity = SportsEntity.fromJson(
        _object(row) ??
            (throw const FormatException('Invalid sporting entity.')),
      );
      if (!ids.add(entity.id)) {
        throw const FormatException('Duplicate sporting entity.');
      }
      return entity;
    }),
  );
}
