# 時間付きGaussianの可逆圧縮

0.8秒・20frameで約14MBのRGS1を、値・順序・数を保ったbyte列転置＋gzipで圧縮する。**可逆性は配信用RGS1に対するもの**で、export時のRGB/quaternion量子化や学習モデルの残像は解消しない。後続のencoder比較は[圧縮比較](temporal-compression-comparison.md)を参照。

## 最近の研究と今回の選択

2026-11-08に確認した一次資料。下記は参考方式で、rangleでの再現結果ではない。

| 研究 | 仕組み / 適用上の距離 |
| --- | --- |
| [TETRIS-4DGS](https://arxiv.org/abs/2610.14663)（2026-10） | 重要度別量子化＋2D属性atlasの画像/動画codec。属性別精度は参考になるが、4D covariance/SHと現速度モデルは異なり、非可逆評価が必要。 |
| [CC-4DGS](https://arxiv.org/abs/2609.02184)（2026-09） | deformation変更＋autoencoder/量子化/codebook。現モデルにdeformation network/高次SHはなく、直接置換できない。 |
| [PD-4DGS](https://arxiv.org/abs/2605.11427)（2026-05） | 静的/global motion/local refinementの段階配信＋rate–distortion学習。ABR候補だが学習表現の変更が必要。 |
| [4DGCPro](https://arxiv.org/abs/2509.17513)（2025-09、NeurIPS 2025記載） | motion-aware階層とentropy/RD学習。複数品質配信の候補で、RGS1 gzipの単純な代替ではない。 |

今回の実装は既存GPU表現・時間評価を保つ小さな基準。研究の表現・学習変更は[後続調査](research/gs-quality-compression-2026.md)に分ける。

## RGS1-Zの契約

[32byteヘッダー](temporal-playback.md)のmagicだけを`RANGL4Z1`へ変え、56byte recordを転置した単一gzip memberを続ける。

```text
columnBytes[b * N + i] = records[i * 56 + b]
```

専用Workerが[`DecompressionStream`](https://compression.spec.whatwg.org/#interface-decompressionstream)で復号し、元record位置へ直接書き戻して`RANGL4D1`へ戻す。[gzip契約](rgs-gzip-contract.md)に従い、追加member・padding・末尾余剰を拒否するためnative gzipに加えてzlib envelopeでも境界を検証する。追加WASM/package、float値の変更、量子化、並べ替えはない。旧RGS1も同じloaderで読み、manifestは`temporal.url`のまま。

ヘッダー検証後に確保し、入力/復号後とも32MiBまで。`N * 56`との長さ不一致、CRC/終端不正は失敗する。GPUを含む端末全体のメモリ制限ではない。

## 公開前のパック

```bash
npm ci --prefix apps/web/viewer
node apps/web/viewer/pack-temporal.mjs input.rgs output.rgs
```

既定packerはgzip level 9、全byte復元一致を確認して新規ファイルへ書く。上書きは禁止し、小さくならなければrawを保持。JSONのサイズ/SHA-256を新revision/manifestへ反映する。Goのリクエスト時圧縮は追加しない。

内部gzipなのでHTTP `Content-Encoding: gzip`は付けない。ETag/SHA/Content-Length/Rangeはパック後bytesが対象。viewerは全体取得後に復号するため、Rangeだけで再生開始できない。初回削減には独立区間・品質ラダー・ABR接続が別途必要。

## 同じモデルでの実測

BlueSquatの同じ2モデルをパックし、全Gaussianと復号SHAを保持した。サイズは10進byte。

| モデル | 元RGS1 | パック後 | 削減 |
| --- | ---: | ---: | ---: |
| depth監督候補・249,895 Gaussian | 13,994,152B | 11,244,896B | 約19.6% |
| 基準・237,321 Gaussian | 13,290,008B | 10,777,965B | 約18.9% |

CPUでraw gzip/shuffle/word-XOR/zstd/Brotliの10候補を比較。word-XORは増量、zstd/Brotliの差はブラウザ依存追加を正当化するほどではなかった。ブラウザ復号性能の測定とは分ける。

Windows Edge / NVIDIA、同カメラ・960×803・DPR1・基準高さ803、2モデル×frame 0/10/19の6組でRGBA全画素・PNG SHAが一致。各session取得1回、再生/loop/orbit/seekの追加取得0、JS/console/HTTP/GLエラー0、離脱後Worker0。

別contextのmain threadで旧gzipのみの復号関数を暖機1回＋5回測定：候補中央値80.5ms/観測p95 89.5ms、基準76.2ms/78.6ms。全6回で復号SHA一致。p95は5標本最大値で母集団保証ではない。HTTP/parse/GPU/初期表示や本番Workerは含まない。これは後続の2-pass境界検証の測定ではなく、その負荷は[gzip契約の検証と負荷](rgs-gzip-contract.md#検証と負荷)を参照する。このEdgeはnative gzip対応、brotli非対応。0.8秒約11.2MBは依然大きく、長尺容量やスマートフォン持続性能は未検証。

## 量子化・間引きの比較のみ行った範囲

RGB8/quaternion8/alpha8を共通に、held-out 4視点×20時刻・960×803、2モデル×元wire/3変換＝640画像をgsplat 1.5.3で描画した。WebGL/orbit評価やcheckpoint評価とは別。

depth候補の画素差は元wire比、8bit尺度。

| 比較変換 | サイズ参考値 | 平均絶対画素差 | 最大画素差 |
| --- | ---: | ---: | ---: |
| 全12float FP16往復 | 約6.33MB* | 0.0583 | 129.3 |
| mu 3float保持・他9float FP16往復 | 約7.58MB* | 0.0292 | 128.0 |
| peak opacity下位10%削除 | 約10.13MB | 0.1442 | 58.0 |

`*` 圧縮済み比較payload＋仮の32byteヘッダー予算で、公開codecではない。削除はRGS1-Z互換ファイルの実サイズ。**全候補未採用**。

FP16のGT平均差は小さくても局所差が残る。10%削除は人物PSNR平均0.00977dB/最悪0.14059dB低下。両モデルとも各候補80/80画像にfloat差があった。

同wire再ロード対照は160/160画像のfloatと保存24PNGが完全一致し、この環境の差を再描画揺らぎでは説明できなかった。一般の決定性保証ではない。採用には顔・手と連続再生の局所評価が必要。
