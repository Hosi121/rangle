import '../community/community_api.dart';
import '../models/publisher_account.dart';

class SubscriptionPage {
  const SubscriptionPage(this.publishers, this.nextCursor);
  final List<PublisherAccount> publishers;
  final String nextCursor;
}

/// Viewer subscriptions use the personal session, independently of staff login.
extension PublicPublisherApi on CommunityApi {
  Future<PublisherAccount> publicPublisher(String id) async {
    final data = await request('GET', 'publishers/${_id(id)}');
    final account = PublisherAccount.fromJson(
      data['publisher'] as Map<String, dynamic>,
    );
    if (account.id != id) throw const FormatException('Publisher mismatch.');
    return account;
  }

  Future<bool> publisherSubscription(String id) async =>
      _subscribed(await request('GET', 'publishers/${_id(id)}/subscription'));

  Future<void> setPublisherSubscription(String id, bool subscribed) async {
    final result = await request(
      subscribed ? 'PUT' : 'DELETE',
      'publishers/${_id(id)}/subscription',
    );
    if (_subscribed(result) != subscribed) {
      throw const FormatException('Subscription state mismatch.');
    }
  }

  Future<SubscriptionPage> publisherSubscriptions({String cursor = ''}) async {
    final url = Uri(
      path: 'me/subscriptions',
      queryParameters: cursor.isEmpty ? null : {'cursor': cursor},
    );
    final data = await request('GET', url.toString());
    final rows = data['publishers'], next = data['next_cursor'];
    if (rows is! List ||
        rows.length > 20 ||
        next is! String ||
        next.length > 256) {
      throw const FormatException('Invalid subscriptions.');
    }
    final ids = <String>{};
    final accounts = rows
        .map((row) => PublisherAccount.fromJson(row as Map<String, dynamic>))
        .toList();
    if (accounts.any((row) => !ids.add(row.id))) {
      throw const FormatException('Duplicate publisher.');
    }
    return SubscriptionPage(List.unmodifiable(accounts), next);
  }
}

String _id(String value) {
  if (!RegExp(r'^[a-z0-9][a-z0-9_-]{0,63}$').hasMatch(value)) {
    throw const FormatException('Invalid publisher ID.');
  }
  return value;
}

bool _subscribed(Map<String, dynamic> data) {
  final value = data['subscribed'];
  if (value is! bool) throw const FormatException('Invalid subscription.');
  return value;
}
