import 'dart:async';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:rangle_web/viewer/thumbnail_preview.dart';
import 'package:rangle_web/viewer/thumbnail_source.dart';

class _Source implements ThumbnailSource {
  final requests = <Duration>[];
  final pending = <Completer<Uint8List?>>[];
  bool controlled = false, fail = false;
  int disposeCount = 0;

  @override
  Future<Uint8List?> frame(Duration position) async {
    requests.add(position);
    if (fail) throw StateError('CORS or codec unavailable');
    if (!controlled) {
      return Uint8List.fromList([position.inMilliseconds ~/ 100]);
    }
    final result = Completer<Uint8List?>();
    pending.add(result);
    return result.future;
  }

  @override
  void dispose() => disposeCount++;
}

void main() {
  testWidgets(
    'idle decoding is released even without pointer exit while its JPEG remains visible',
    (tester) async {
      final sources = <_Source>[];
      final preview = ThumbnailPreview(() {
        final source = _Source();
        sources.add(source);
        return source;
      });
      preview.preview(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 80));
      await tester.pump(const Duration(milliseconds: 999));
      expect(sources.single.disposeCount, 0);
      expect(preview.image, [10]);
      await tester.pump(const Duration(milliseconds: 1));
      expect(sources.single.disposeCount, 1);
      expect(preview.image, [10]);
      preview.preview(const Duration(seconds: 1));
      expect(sources.length, 1);
      expect(preview.image, [10]);
      preview.preview(const Duration(seconds: 2));
      await tester.pump(const Duration(milliseconds: 80));
      expect(sources.length, 2);
      expect(preview.image, [20]);
      preview.dispose();
      expect(sources.map((source) => source.disposeCount), [1, 1]);
    },
  );

  testWidgets(
    'hide releases media while cached images remain reusable without a source',
    (tester) async {
      final sources = <_Source>[];
      final preview = ThumbnailPreview(() {
        final source = _Source();
        sources.add(source);
        return source;
      });
      preview.preview(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 80));
      expect(preview.image, [10]);
      preview.hide();
      expect(sources.single.disposeCount, 1);
      preview.preview(const Duration(seconds: 1));
      expect(preview.image, [10]);
      await tester.pump(const Duration(milliseconds: 80));
      expect(sources.length, 1);
      preview.preview(const Duration(seconds: 2));
      await tester.pump(const Duration(milliseconds: 80));
      expect(sources.length, 2);
      expect(preview.image, [20]);
      preview.hide();
      preview.hide();
      preview.dispose();
      expect(sources.map((s) => s.disposeCount), [1, 1]);
    },
  );

  testWidgets(
    'hide and reentry do not wait for a cancelled decoder or publish its old completion',
    (tester) async {
      final sources = <_Source>[];
      final preview = ThumbnailPreview(() {
        final source = _Source()..controlled = true;
        sources.add(source);
        return source;
      });
      preview.preview(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 80));
      preview.hide();
      preview.preview(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 80));
      expect(sources.length, 2);
      sources.first.pending.single.complete(Uint8List.fromList([99]));
      await tester.pump();
      expect(preview.image, isNull);
      preview.preview(const Duration(seconds: 2));
      await tester.pump(const Duration(milliseconds: 80));
      // The old job's finally block cannot clear the new job's busy flag.
      expect(sources.last.requests, [const Duration(seconds: 1)]);
      sources.last.pending.first.complete(Uint8List.fromList([1]));
      await tester.pump();
      expect(sources.last.requests, [
        const Duration(seconds: 1),
        const Duration(seconds: 2),
      ]);
      expect(preview.image, isNull);
      sources.last.pending.last.complete(Uint8List.fromList([2]));
      await tester.pump();
      expect(preview.image, [2]);
      preview.dispose();
    },
  );

  testWidgets('continuous hover movement decodes latest frames every 80ms', (
    tester,
  ) async {
    final source = _Source();
    final preview = ThumbnailPreview(() => source);
    try {
      for (var i = 0; i < 30; i++) {
        preview.preview(Duration(milliseconds: i * 16));
        await tester.pump(const Duration(milliseconds: 16));
        // Check progress during movement, before any idle time is allowed.
        expect(source.requests.length, (i + 1) ~/ 5);
      }
      expect(source.requests, [
        for (var i = 0; i < 6; i++) Duration(milliseconds: 64 + i * 80),
      ]);
      expect(preview.image, [4]);
    } finally {
      preview.dispose();
    }
  });

  testWidgets('thumbnail decoding is lazy and coalesces fast hover movement', (
    tester,
  ) async {
    final source = _Source();
    var created = 0;
    final preview = ThumbnailPreview(() {
      created++;
      return source;
    });
    expect(created, 0);
    preview.preview(const Duration(milliseconds: 1030));
    preview.preview(const Duration(milliseconds: 2550));
    preview.preview(const Duration(milliseconds: 3260));
    expect(source.requests, isEmpty);
    await tester.pump(const Duration(milliseconds: 80));
    expect(created, 1);
    expect(source.requests, [const Duration(milliseconds: 3260)]);
    expect(preview.image, [32]);
    preview.dispose();
    expect(source.disposeCount, 1);
  });

  testWidgets(
    'only latest thumbnail is published and hidden/disposed work stays hidden',
    (tester) async {
      final source = _Source()..controlled = true;
      final resumed = _Source()..controlled = true;
      var created = 0;
      final preview = ThumbnailPreview(() => created++ == 0 ? source : resumed);
      preview.preview(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 80));
      preview.preview(const Duration(seconds: 2));
      await tester.pump(const Duration(milliseconds: 80));
      preview.preview(const Duration(seconds: 3));
      await tester.pump(const Duration(milliseconds: 80));
      expect(source.requests, [const Duration(seconds: 1)]);
      source.pending.first.complete(Uint8List.fromList([1]));
      await tester.pump();
      expect(preview.image, isNull);
      expect(source.requests, [
        const Duration(seconds: 1),
        const Duration(seconds: 3),
      ]);
      source.pending.last.complete(Uint8List.fromList([3]));
      await tester.pump();
      expect(preview.image, [3]);
      preview.preview(const Duration(seconds: 4));
      await tester.pump(const Duration(milliseconds: 80));
      preview.hide();
      expect(source.disposeCount, 1);
      source.pending.last.complete(Uint8List.fromList([4]));
      await tester.pump();
      expect(preview.image, isNull);
      preview.preview(const Duration(seconds: 5));
      await tester.pump(const Duration(milliseconds: 80));
      preview.dispose();
      resumed.pending.last.complete(Uint8List.fromList([5]));
      await tester.pump();
      expect(source.disposeCount, 1);
      expect(resumed.disposeCount, 1);
      expect(tester.takeException(), isNull);
    },
  );

  testWidgets(
    'thumbnail cache retains at most 16 frames and reuses recent frames',
    (tester) async {
      final source = _Source();
      final preview = ThumbnailPreview(() => source);
      for (var i = 0; i < 17; i++) {
        preview.preview(Duration(seconds: i));
        await tester.pump(const Duration(milliseconds: 80));
      }
      expect(source.requests.length, 17);
      preview.preview(const Duration(seconds: 15));
      await tester.pump(const Duration(milliseconds: 80));
      expect(source.requests.length, 17);
      preview.preview(Duration.zero);
      await tester.pump(const Duration(milliseconds: 80));
      expect(source.requests.length, 18);
      preview.dispose();
    },
  );

  testWidgets(
    'decoder errors fall back without throwing and queued work cancels on dispose',
    (tester) async {
      final source = _Source()..fail = true;
      final preview = ThumbnailPreview(() => source);
      preview.preview(const Duration(seconds: 1));
      await tester.pump(const Duration(milliseconds: 80));
      expect(preview.image, isNull);
      preview.preview(const Duration(seconds: 2));
      preview.dispose();
      await tester.pump(const Duration(seconds: 1));
      expect(source.requests, [const Duration(seconds: 1)]);
      expect(tester.takeException(), isNull);
    },
  );
}
