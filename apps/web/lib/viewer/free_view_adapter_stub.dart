import 'package:flutter/widgets.dart';

import 'viewer_adapter.dart';

ViewerAdapter createAdapter(Uri source) => _UnsupportedAdapter();
ViewerAdapter createLiveAdapter(
  Uri source, {
  required List<String> origins,
  String? sha256,
}) => _UnsupportedAdapter();

class _UnsupportedAdapter extends ViewerAdapter {
  @override
  ViewerValue get value => const ViewerValue(status: ViewerStatus.error);
  @override
  Future<void> initialize() async {}
  @override
  Future<void> pause() async {}
  @override
  Future<void> seek(Duration position) async {}
  @override
  Future<void> toggleMute() async {}
  @override
  Future<void> togglePlayback() async {}
  @override
  Widget buildView() => const SizedBox.shrink();
}
