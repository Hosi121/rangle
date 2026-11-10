import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/live_source_updates.dart';

final initial = Uri.parse('https://example.test/initial/scene.json');
Uri scene(int revision) =>
    Uri.parse('https://example.test/$revision/scene.json');

void main() {
  late LiveSourceUpdates updates;
  late List<LiveSourceUpdate> starts;
  late int cancels;
  setUp(() {
    starts = [];
    cancels = 0;
    updates = LiveSourceUpdates(
      initialSource: initial,
      initialSha256: 'initial',
      start: starts.add,
      cancel: () => cancels++,
    );
  });
  tearDown(() => updates.close());

  test(
    'active draw progresses before the latest waiting metadata starts',
    () async {
      updates.initialized();
      final active = updates.update(scene(1), 'one');
      final obsolete = updates.update(scene(2), 'two');
      final latest = updates.update(scene(3), 'three');
      expect(await obsolete, isFalse);
      expect(starts.map((request) => request.source), [scene(1)]);
      expect(cancels, 0);
      updates.finish(starts.first.id, scene(1).toString(), true);
      expect(await active, isTrue);
      expect(starts.map((request) => request.source), [scene(1), scene(3)]);
      updates.finish(starts.last.id, scene(3).toString(), true);
      expect(await latest, isTrue);
    },
  );

  test(
    'initial loading coalesces metadata and settles superseded futures',
    () async {
      final obsolete = updates.update(scene(1), 'one');
      final latest = updates.update(scene(2), 'two');
      expect(await obsolete, isFalse);
      expect(starts, isEmpty);
      updates.initialized();
      expect(starts.single.source, scene(2));
      updates.finish(starts.single.id, scene(2).toString(), true);
      expect(await latest, isTrue);
    },
  );

  test(
    'duplicate active, waiting and displayed sources share acquisition',
    () async {
      final initialUpdate = updates.update(initial, 'initial');
      updates.initialized();
      expect(await initialUpdate, isTrue);
      expect(starts, isEmpty);
      final active = updates.update(scene(1), 'one');
      expect(identical(updates.update(scene(1), 'one'), active), isTrue);
      final waiting = updates.update(scene(2), 'two');
      expect(identical(updates.update(scene(2), 'two'), waiting), isTrue);
      updates.finish(starts.first.id, scene(1).toString(), true);
      expect(await active, isTrue);
      updates.finish(starts.last.id, scene(2).toString(), true);
      expect(await waiting, isTrue);
      expect(await updates.update(scene(2), 'two'), isTrue);
      expect(starts, hasLength(2));
    },
  );

  test(
    'request ID and source must both match before acknowledging a draw',
    () async {
      updates.initialized();
      final active = updates.update(scene(1), 'one');
      final request = starts.single;
      updates.finish(request.id.toDouble(), scene(1).toString(), true);
      updates.finish(request.id, scene(2).toString(), true);
      updates.finish(request.id + 1, scene(1).toString(), true);
      expect(updates.active, same(request));
      updates.finish(request.id, scene(1).toString(), true);
      expect(await active, isTrue);
      updates.finish(request.id, scene(1).toString(), false);
      expect(updates.active, isNull);
    },
  );

  testWidgets('timeout cancels the active candidate and advances once', (
    tester,
  ) async {
    updates.initialized();
    final active = updates.update(scene(1), 'one');
    final latest = updates.update(scene(2), 'two');
    await tester.pump(const Duration(seconds: 45));
    expect(await active, isFalse);
    expect(cancels, 1);
    expect(starts.map((request) => request.source), [scene(1), scene(2)]);
    updates.finish(starts.first.id, scene(1).toString(), true);
    expect(updates.active, same(starts.last));
    updates.finish(starts.last.id, scene(2).toString(), true);
    expect(await latest, isTrue);
    await tester.pump(const Duration(seconds: 90));
    expect(cancels, 1);
    expect(starts, hasLength(2));
  });

  testWidgets(
    'hidden state cancels both slots without retries or stale completion',
    (tester) async {
      updates.initialized();
      final active = updates.update(scene(1), 'one');
      final pending = updates.update(scene(2), 'two');
      updates.visible(false);
      expect(await active, isFalse);
      expect(await pending, isFalse);
      expect(await updates.update(scene(3), 'three'), isFalse);
      updates.finish(starts.first.id, scene(1).toString(), true);
      updates.visible(true);
      await tester.pump(const Duration(seconds: 90));
      expect(starts, hasLength(1));
      expect(cancels, 1);
      final retry = updates.update(scene(3), 'three');
      updates.finish(starts.last.id, scene(3).toString(), true);
      expect(await retry, isTrue);
    },
  );

  test('dispose or session epoch change settles both futures and rejects late work', () async {
    updates.initialized();
    final active = updates.update(scene(1), 'one');
    final pending = updates.update(scene(2), 'two');
    updates.close();
    expect(await active, isFalse);
    expect(await pending, isFalse);
    updates.finish(starts.first.id, scene(1).toString(), true);
    updates.initialized();
    expect(await updates.update(scene(3), 'three'), isFalse);
    expect(starts, hasLength(1));
    expect(cancels, 1);
  });

  test(
    'failed candidate advances to latest and a changed hash is a new request',
    () async {
      updates.initialized();
      final failed = updates.update(scene(1), 'invalid');
      final latest = updates.update(scene(1), 'valid');
      updates.finish(starts.first.id, scene(1).toString(), false);
      expect(await failed, isFalse);
      expect(starts.map((request) => request.sha256), ['invalid', 'valid']);
      updates.finish(starts.last.id, scene(1).toString(), true);
      expect(await latest, isTrue);
    },
  );
}
