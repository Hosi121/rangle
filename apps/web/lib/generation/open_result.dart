import 'open_result_stub.dart'
    if (dart.library.js_interop) 'open_result_web.dart'
    as platform;

bool openGeneratedViewer(Uri uri) => platform.open(uri);
