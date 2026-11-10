import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/viewer_adapter.dart';
import 'package:rangle_web/viewer/viewer_selection.dart';

void main() {
  test('equal snapshots suppress duplicates; only the playhead projection ignores position', () {
    final source = ValueNotifier(const ViewerValue());
    final full = ViewerSelection(source, (v) => v);
    final presentation = ViewerSelection(source, (v) => v.presentation);
    var updates = 0, presentations = 0;
    full.addListener(() => updates++);
    presentation.addListener(() => presentations++);
    source.value = const ViewerValue();
    expect(updates, 0);
    source.value = const ViewerValue(position: Duration(seconds: 1));
    expect(updates, 1);
    expect(presentations, 0);

    // Every other field can change rendering, controls or a media handoff.
    final changes = <ViewerValue>[
      const ViewerValue(status: ViewerStatus.ready),
      const ViewerValue(duration: Duration(seconds: 20)),
      const ViewerValue(playing: true),
      const ViewerValue(canPlay: false),
      const ViewerValue(muted: true),
      const ViewerValue(buffering: true),
      ViewerValue(source: Uri.parse('https://example.invalid/new.json')),
      const ViewerValue(prepared: true),
      const ViewerValue(
        timeline: ViewerTimeline(frameCount: 20, frameRate: 25),
      ),
      const ViewerValue(playIntent: true),
      const ViewerValue(aspectRatio: 16 / 9),
      const ViewerValue(rotationCorrection: 90),
    ];
    for (final value in changes) {
      source.value = const ViewerValue();
      final before = presentations;
      source.value = value;
      expect(presentations, before + 1);
      expect(full.value, value);
    }
    presentation.dispose();
    full.dispose();
    // Releasing projections detaches them; later source events are harmless.
    source.value = const ViewerValue(playing: true);
    source.dispose();
  });
}
