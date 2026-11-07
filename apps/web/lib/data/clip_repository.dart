import '../models/clip.dart';

abstract interface class ClipRepository {
  Future<List<Clip>> load();
}

/// The first entry uses the bundled research sample unless a video URL overrides it.
class DemoClipRepository implements ClipRepository {
  const DemoClipRepository({this.previewUrl = '', this.sceneUrl = ''});

  final String previewUrl;
  final String sceneUrl;

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
    final scene = sceneUrl.trim();
    final sceneUri = scene.isEmpty ? null : Uri.tryParse(scene);
    if (scene.isNotEmpty &&
        (sceneUri == null ||
            !sceneUri.hasAuthority ||
            !{'http', 'https'}.contains(sceneUri.scheme))) {
      throw const FormatException('Scene URL must use HTTP or HTTPS.');
    }
    return [
      Clip(
        id: 'demo-01',
        title: value.isEmpty ? 'VRU Basketball' : 'プレビュー映像',
        sport: 'バスケットボール',
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
