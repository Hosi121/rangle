import '../models/clip.dart';

abstract interface class ClipRepository {
  Future<List<Clip>> load();
}

/// Explicit demo metadata: there are no bundled videos or sports photographs.
class DemoClipRepository implements ClipRepository {
  const DemoClipRepository({this.previewUrl = ''});

  final String previewUrl;

  @override
  Future<List<Clip>> load() async {
    final value = previewUrl.trim();
    final uri = value.isEmpty ? null : Uri.tryParse(value);
    if (value.isNotEmpty &&
        (uri == null ||
            !uri.hasAuthority ||
            !{'http', 'https'}.contains(uri.scheme))) {
      throw const FormatException('Preview URL must use HTTP or HTTPS.');
    }
    return [
      Clip(
        id: 'demo-01',
        title: value.isEmpty ? 'バスケットボール 01' : 'プレビュー映像',
        sport: value.isEmpty ? 'バスケットボール' : 'プレビュー',
        description: value.isEmpty
            ? '表示と操作を確認するためのデモ項目です。映像の公開をお待ちください。'
            : '通常の動画プレビューです。自由視点の再生には対応していません。',
        previewUrl: uri,
      ),
      const Clip(
        id: 'demo-02',
        title: 'バスケットボール 02',
        sport: 'バスケットボール',
        description: '表示と操作を確認するためのデモ項目です。映像の公開をお待ちください。',
      ),
      const Clip(
        id: 'demo-03',
        title: 'バスケットボール 03',
        sport: 'バスケットボール',
        description: '表示と操作を確認するためのデモ項目です。映像の公開をお待ちください。',
      ),
    ];
  }
}
