import 'package:web/web.dart' as web;

bool open(Uri uri) {
  web.window.location.assign(uri.toString());
  return true;
}
