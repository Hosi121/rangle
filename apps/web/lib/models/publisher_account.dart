/// Public identity of a company or organization. This is separate from the
/// signed-in AccountUser and contains no credentials or staff privileges.
class PublisherAccount {
  const PublisherAccount({
    required this.id,
    required this.name,
    required this.kind,
  });
  final String id, name, kind;

  factory PublisherAccount.fromJson(Map<String, dynamic> data) {
    final id = data['id'], name = data['name'], kind = data['kind'];
    if (id is! String ||
        !RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(id) ||
        name is! String ||
        name.trim().isEmpty ||
        name.runes.length > 120 ||
        RegExp(r'[\x00-\x1f\x7f-\x9f]').hasMatch(name) ||
        (kind != 'company' && kind != 'organization')) {
      throw const FormatException('Invalid publisher account.');
    }
    return PublisherAccount(id: id, name: name, kind: kind as String);
  }
}
