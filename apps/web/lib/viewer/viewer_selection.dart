import 'package:flutter/foundation.dart';

import 'viewer_adapter.dart';

/// A projection that notifies only when the selected viewer state changes.
/// The owner must dispose it before releasing its source.
class ViewerSelection<T> extends ChangeNotifier implements ValueListenable<T> {
  ViewerSelection(this._source, this._select)
    : _value = _select(_source.value) {
    _source.addListener(_changed);
  }

  final ValueListenable<ViewerValue> _source;
  final T Function(ViewerValue) _select;
  T _value;

  @override
  T get value => _value;

  void _changed() {
    final next = _select(_source.value);
    if (next == _value) return;
    _value = next;
    notifyListeners();
  }

  @override
  void dispose() {
    _source.removeListener(_changed);
    super.dispose();
  }
}
