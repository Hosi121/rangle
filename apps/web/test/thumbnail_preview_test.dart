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
      final preview = ThumbnailPreview(() => source);
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
      source.pending.last.complete(Uint8List.fromList([4]));
      await tester.pump();
      expect(preview.image, isNull);
      preview.preview(const Duration(seconds: 5));
      await tester.pump(const Duration(milliseconds: 80));
      preview.dispose();
      source.pending.last.complete(Uint8List.fromList([5]));
      await tester.pump();
      expect(source.disposeCount, 1);
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
