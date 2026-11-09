import '../models/clip.dart';

abstract interface class ClipRepository {
  Future<List<Clip>> load({String query = '', String tag = '', String? clipId});
}

/// A known catalog change asks the next load to revalidate its HTTP cache.
abstract interface class RefreshableClipRepository implements ClipRepository {
  void invalidateCache();
}

/// One public catalog page. A null cursor alone denotes the server's end.
class ClipPage {
  const ClipPage(this.clips, {this.nextCursor});
  final List<Clip> clips;
  final String? nextCursor;
}

/// Optional paging contract; existing repositories can keep implementing load.
abstract interface class PagedClipRepository implements ClipRepository {
  Future<ClipPage> loadPage({
    String query = '',
    String tag = '',
    String? clipId,
    String? cursor,
    int limit = 20,
    Future<void>? abortTrigger,
  });
}

/// A cursor rejected after a catalog/origin change must restart at page one.
class StaleClipCursor implements Exception {
  const StaleClipCursor();
}

/// The first entry uses the bundled research sample unless a video URL overrides it.
class DemoClipRepository implements ClipRepository {
  const DemoClipRepository({this.previewUrl = '', this.sceneUrl = ''});

  final String previewUrl;
  final String sceneUrl;

  @override
  Future<List<Clip>> load({
    String query = '',
    String tag = '',
    String? clipId,
  }) async {
    final value = previewUrl.trim();
    final uri = value.isEmpty ? null : Uri.tryParse(value);
    if (value.isNotEmpty &&
        (uri == null ||
            !uri.hasAuthority ||
            !{'http', 'https'}.contains(uri.scheme))) {
      throw const FormatException('Preview URL must use HTTP or HTTPS.');
    }
    final scene = sceneUrl.trim();
    final sceneUri = scene.isEmpty ? null : Uri.tryParse(scene);
    if (scene.isNotEmpty &&
        (sceneUri == null ||
            !sceneUri.hasAuthority ||
            !{'http', 'https'}.contains(sceneUri.scheme))) {
      throw const FormatException('Scene URL must use HTTP or HTTPS.');
    }
    final clips = [
      Clip(
        id: 'demo-01',
        title: value.isEmpty ? 'VRU Basketball' : 'プレビュー映像',
        sport: 'バスケットボール',
        tags: const ['バスケットボール', '自由視点'],
        description: value.isEmpty
            ? '短い再構成サンプル。AVS-VRU / Jiahao Wu ほか（CC BY 4.0）'
            : '通常の動画プレビューです。',
        previewUrl: uri ?? Uri.parse('assets/samples/basketball.webm'),
        sceneUrl: sceneUri,
      ),
      const Clip(
        id: 'demo-02',
        title: 'バスケットボール 02',
        sport: 'バスケットボール',
        tags: ['バスケットボール'],
        description: '表示と操作を確認するためのデモ項目です。映像の公開をお待ちください。',
      ),
      const Clip(
        id: 'demo-03',
        title: 'バスケットボール 03',
        sport: 'バスケットボール',
        tags: ['バスケットボール'],
        description: '表示と操作を確認するためのデモ項目です。映像の公開をお待ちください。',
      ),
    ];
    final terms = query
        .toLowerCase()
        .trim()
        .split(RegExp(r'\s+'))
        .where((term) => term.isNotEmpty);
    final filtered = clips
        .where(
          (clip) =>
              (tag.isEmpty ||
                  clip.tags.any(
                    (value) => value.toLowerCase() == tag.toLowerCase(),
                  )) &&
              terms.every(
                (term) =>
                    '${clip.title} ${clip.sport} ${clip.description} ${clip.tags.join(' ')}'
                        .toLowerCase()
                        .contains(term),
              ),
        )
        .toList();
    if (clipId != null) {
      final index = filtered.indexWhere((clip) => clip.id == clipId);
      if (index < 0) throw const FormatException('Shared clip not found.');
      final shared = filtered.removeAt(index);
      filtered.insert(0, shared);
    }
    return filtered;
  }
}
