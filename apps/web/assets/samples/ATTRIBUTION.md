# Basketball sample

`basketball.webm` は VRU Basketball / AVS-VRU のGuangzhou (`gz`) シーンを基にした、0.8秒の固定視点プレビューです。

- データ: Jiahao Wu ほか。[VRU Basketball](https://huggingface.co/datasets/BestWJH/VRU_Basketball/tree/8592b0ddd27938e2c12dbd592fca2ecca54dec38)、[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)。
- 変更: frame 0–19を歪み補正・縮小し、LocalDyGSで再構成。camera 0から480×270・25fpsで描画した既存MP4をVP9/WebMへ変換。
- 再構成実装: [LocalDyGS](https://github.com/WuJH2001/LocalDyGS/tree/39dacdcd8ef6d2b93824df79041713b4a29fb828)。コード・重みには同実装の[利用条件](https://github.com/WuJH2001/LocalDyGS/blob/39dacdcd8ef6d2b93824df79041713b4a29fb828/LICENSE)が適用されます。

この媒体の出典と利用条件は、アプリの自作コードに適用するMIT Licenseと区別します。
