import 'viewer_adapter.dart';
import 'free_view_adapter_stub.dart'
    if (dart.library.js_interop) 'free_view_adapter_web.dart'
    as platform;

ViewerAdapter createFreeViewAdapter(Uri source) =>
    platform.createAdapter(source);

ViewerAdapter createLiveFreeViewAdapter(
  Uri source, {
  required List<String> origins,
  String? sha256,
}) => platform.createLiveAdapter(source, origins: origins, sha256: sha256);
