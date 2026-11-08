import 'package:flutter/services.dart';

import 'share_clip.dart';

Future<ShareResult> share(String title, Uri url) async {
  await Clipboard.setData(ClipboardData(text: url.toString()));
  return ShareResult.copied;
}
