import 'dart:js_interop';

import 'package:web/web.dart' as web;

@JS()
extension type _HapticNavigator(JSObject _) implements JSObject {
  external JSFunction? get vibrate;
}

void pulse(int milliseconds) {
  try {
    final navigator = _HapticNavigator(web.window.navigator);
    navigator.vibrate?.callAsFunction(navigator, milliseconds.toJS);
  } catch (_) {
    /* Browsers without vibration keep all other interactions. */
  }
}
