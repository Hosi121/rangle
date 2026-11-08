import 'dart:js_interop';

import 'package:flutter/services.dart';
import 'package:web/web.dart' as web;

import 'share_clip.dart';

Future<ShareResult> share(String title, Uri url) async {
  final data = web.ShareData(title: title, url: url.toString());
  try {
    if (web.window.navigator.canShare(data)) {
      await web.window.navigator.share(data).toDart;
      return ShareResult.shared;
    }
  } catch (error) {
    try {
      final jsError = error as JSObject;
      if (jsError.isA<web.DOMException>() &&
          (jsError as web.DOMException).name == 'AbortError') {
        return ShareResult.cancelled;
      }
    } catch (_) {
      /* Fall back to copying the link when sharing is unavailable. */
    }
  }
  await Clipboard.setData(ClipboardData(text: url.toString()));
  return ShareResult.copied;
}
