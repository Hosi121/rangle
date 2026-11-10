import 'dart:convert';

import 'package:http/http.dart' as http;

Map<String, dynamic> publisherIdentity({
  String account = 'club-a',
  String role = 'editor',
}) => {
  'account': {'id': account, 'name': 'デモ団体', 'kind': 'organization'},
  'staff': {
    'id': 'staff-a',
    'username': 'editor',
    'role': role,
    'disabled': false,
  },
};
http.Response publisherJson(Object data, [int status = 200]) => http.Response(
  jsonEncode(data),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);
