import 'dart:convert';

/// Check decoded key aliases before native jsonDecode discards duplicate fields.
Object? strictJsonDecode(
  String text, {
  int maxBytes = 524288,
  int maxDepth = 16,
}) {
  if (utf8.encode(text).length > maxBytes) {
    throw const FormatException('JSON exceeds limit.');
  }
  final scanner = _KeyScanner(text, maxDepth)..value(0);
  scanner.space();
  if (scanner.at != text.length) {
    throw const FormatException('Expected one JSON document.');
  }
  return jsonDecode(text);
}

class _KeyScanner {
  _KeyScanner(this.text, this.maxDepth);
  final String text;
  final int maxDepth;
  int at = 0;
  String get next => at < text.length ? text[at] : '';
  void space() {
    while (next.isNotEmpty && ' \r\n\t'.contains(next)) {
      at++;
    }
  }

  String string() {
    final start = at++;
    while (at < text.length) {
      if (next == r'\') {
        at += 2;
      } else if (text[at++] == '"') {
        return jsonDecode(text.substring(start, at)) as String;
      }
    }
    throw const FormatException('Unterminated JSON string.');
  }

  void expect(String character) {
    if (next != character) throw const FormatException('Invalid JSON.');
    at++;
  }

  void value(int depth) {
    if (depth > maxDepth) {
      throw const FormatException('JSON nesting exceeds limit.');
    }
    space();
    if (next == '{') {
      at++;
      space();
      final keys = <String>{};
      if (next != '}') {
        while (true) {
          if (next != '"') throw const FormatException('Invalid JSON key.');
          if (!keys.add(string())) {
            throw const FormatException('Duplicate JSON key.');
          }
          space();
          expect(':');
          value(depth + 1);
          space();
          if (next != ',') break;
          at++;
          space();
        }
      }
      expect('}');
    } else if (next == '[') {
      at++;
      space();
      if (next != ']') {
        while (true) {
          value(depth + 1);
          space();
          if (next != ',') break;
          at++;
        }
      }
      expect(']');
    } else if (next == '"') {
      string();
    } else {
      final start = at;
      while (next.isNotEmpty && !' \r\n\t,}]'.contains(next)) {
        at++;
      }
      if (!RegExp(
        r'^(?:null|true|false|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)$',
      ).hasMatch(text.substring(start, at))) {
        throw const FormatException('Invalid JSON value.');
      }
    }
  }
}
