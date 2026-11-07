import 'viewer_adapter.dart';
import 'free_view_adapter_stub.dart'
    if (dart.library.js_interop) 'free_view_adapter_web.dart'
    as platform;

ViewerAdapter createFreeViewAdapter(Uri source) =>
    platform.createAdapter(source);
