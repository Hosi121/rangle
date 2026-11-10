import '../models/clip.dart';
import 'share_fallback.dart'
    if (dart.library.js_interop) 'share_web.dart'
    as platform;

enum ShareResult { shared, copied, cancelled }

typedef ClipShareAction = Future<ShareResult> Function(Clip clip);
Uri clipLink(Uri base, String id) => base
    .replace(path: '/demo/', queryParameters: {'clip': id})
    .removeFragment();
Future<ShareResult> shareClip(Clip clip) =>
    platform.share(clip.title, clipLink(Uri.base, clip.id));
