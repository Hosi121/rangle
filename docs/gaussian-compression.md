# 時間付きGaussianの可逆圧縮

0.8秒・20frameのRGS1は約14MBある。まず、学習済みの位置・速度・scale・時間・色を変えずに転送量を減らす。画質低下を伴う量子化と間引きは比較対象とし、デフォルトの配信へは採用しない。

## 最近の研究と今回の選択

2026-11-08時点で一次資料を確認した。以下の論文の圧縮率を、このモデルで再現した結果として扱わない。

| 研究 | 主な仕組み | rangleへの適用判断 |
| --- | --- | --- |
| [TETRIS-4DGS](https://arxiv.org/abs/2610.14663)（2026-10） | 重要度に応じた量子化と、属性を2Dへ並べた画像・動画codecでの圧縮 | 位置に敏感な属性とその他を分ける設計が参考になる。元の4D covariance・SH表現は現在の速度モデルと異なり、非可逆部分の画質比較が必要 |
| [CC-4DGS](https://arxiv.org/abs/2609.02184)（2026-09） | deformation fieldの変更、属性のautoencoder・量子化・codebook | 現在はdeformation networkも高次SHも持たないため、そのまま置き換える圧縮器ではない |
| [PD-4DGS](https://arxiv.org/abs/2605.11427)（2026-05） | 静的・global motion・local refinementの段階配信とrate–distortion学習 | 初期表示と全品質の転送量を分ける方向はABRに合う。変形networkを含む学習表現の変更が必要で、今回の可逆圧縮とは別の比較になる |
| [4DGCPro](https://arxiv.org/abs/2509.17513)（2025-09、NeurIPS 2025記載） | motion-awareな階層表現と、階層別のentropy・rate–distortion学習 | 1モデルから複数品質を配信する設計の候補。現在のRGS1を単純にgzipする方式の代替として即採用はしない |

今回の実装はこれらの再現ではない。現在のモデルに適用できる小さな基準として、属性の同じbyte位置を連続させ、標準gzipで圧縮する。Gaussianの順序・数・値は復元時にすべて戻す。通信量だけを減らし、GPUへ渡す表現と時間評価は変えない。

## RGS1-Zの契約

元の[32byteヘッダー](temporal-playback.md)を保持し、先頭8byteだけを`RANGL4Z1`とする。続くpayloadは56byteのrecordをbyte列ごとに転置してgzipにした単一memberである。

```text
columnBytes[b * N + i] = records[i * 56 + b]
```

復号時はgzip出力を読みながら元のrecord位置へ直接書き戻し、先頭magicを`RANGL4D1`へ戻す。浮動小数点の演算・量子化・Gaussianの並べ替えは行わない。元のRGS1も同じloaderで読めるため、scene manifestは引き続き`temporal.url`で`.rgs`を指定する。

取得・復号は専用Workerで行う。ブラウザの[`DecompressionStream`](https://compression.spec.whatwg.org/#interface-decompressionstream)を使い、追加のWASM codecや依存packageは導入しない。ヘッダーを検証してから出力領域を確保し、入力・復号後とも32MiBまでとする。宣言された`N * 56`と出力長が違う場合、gzipのCRC・終端が不正な場合は失敗させる。これは資産単体の上限であり、GPU資源を含む端末全体のメモリ上限ではない。

## 公開前のパック

Node依存の導入後、公開工程で一度だけ実行する。

```bash
npm ci --prefix apps/web/viewer
node apps/web/viewer/pack-temporal.mjs input.rgs output.rgs
```

packerはgzip level 9を使い、復号結果の全byte一致を確認してから新しいファイルへ書く。既存ファイルへの上書きは禁止する。圧縮しても小さくならない場合は元のRGS1を保持する。出力するJSONに圧縮前後のサイズとSHA-256を含め、公開先の新revision・manifestへ反映する。リクエストごとの圧縮処理はGoサーバーへ追加しない。

これは独自ヘッダーを持つ資産内部の圧縮であり、HTTPの`Content-Encoding: gzip`を付ける形式ではない。HTTP/CDNはパック後のbytesを通常の版付き資産として配信する。ETag・SHA・Content-Length・Rangeの対象もパック後のbytesである。現在のviewerは資産全体を取得して復号するため、任意のRangeだけで描画を開始できる形式ではない。初回転送量をさらに減らす独立区間・品質ラダー・ABR接続は別途必要となる。

## 同じモデルでの実測

既存BlueSquatの同じ2モデルをパックした。元データの全Gaussianを保持し、復号結果のSHA-256は元ファイルと一致した。サイズは10進表記。

| モデル | 元RGS1 | パック後 | 削減 |
| --- | ---: | ---: | ---: |
| depth監督の候補・249,895 Gaussian | 13,994,152B | 11,244,896B | 約19.6% |
| 比較基準・237,321 Gaussian | 13,290,008B | 10,777,965B | 約18.9% |

CPU参考比較ではraw gzip、byte-shuffle、word-XOR、zstd、Brotliの10候補を同じモデルで検証した。word-XORはshuffle単独より容量が増え、zstd/Brotliはブラウザ実装の追加条件を正当化するほどの容量差がなかった。これらのCPU測定をブラウザの復号性能とみなさない。

Windows Edge / NVIDIAで同じカメラ・960×803・DPR 1・基準高さ803に揃え、両モデルのframe 0/10/19を比較した。全6組でRGBAの全画素が一致し、PNGのSHAも一致した。パック後のモデルは各sessionで1回だけ取得し、再生・loop・orbit・seek中の追加取得は0回。JavaScript・console・HTTP・GLエラーは0、離脱後のWorkerは0だった。

別contextで、同じ復号関数をページのmain threadから実行して暖機1回＋5回を測定した。候補の復号中央値80.5ms・観測p95 89.5ms、基準は76.2ms・78.6msで、全6回の復号SHAは元データと一致した。ここでのp95は5標本の最大値を使う記述値で、母集団の裾の保証ではない。HTTP取得・parse・GPU・初期表示を含む時間や、本番Workerでの測定ではない。このEdgeではgzipのnative constructorは利用でき、brotliは利用できなかった。スマートフォン実機への性能の外挿は行わない。

今回の可逆性は既存の配信用RGS1に対するもの。RGS1へ書き出す前のRGB・quaternionの量子化や、学習モデルに残る顔・手の残像がなくなるわけではない。0.8秒で約11.2MBは依然大きく、長い映像へ単純に比例させた容量やスマートフォンでの持続性能は未検証である。

## 量子化・間引きの比較のみ行った範囲

元wireのRGB8・quaternion8・alpha8を共通にし、同じheldout 4視点×20時刻・960×803で比較した。候補・基準の両モデルについて、元wireと3変換の計640画像をgsplat 1.5.3で描画した。WebGL表示や任意のorbitに対する評価ではなく、学習時のcheckpoint評価と数値を混ぜない。

depth監督のモデルでは次の結果となった。画素差は元wireの描画との比較で、8bit値の尺度を使う。

| 比較用の変換 | サイズの参考値 | 平均絶対画素差 | 最大画素差 |
| --- | ---: | ---: | ---: |
| 全12floatをFP16へ往復 | 約6.33MB* | 0.0583 | 129.3 |
| muの3floatを維持し、他9floatをFP16へ往復 | 約7.58MB* | 0.0292 | 128.0 |
| peak opacity下位10%を削除 | 約10.13MB | 0.1442 | 58.0 |

`*` FP16の比較用payloadは実際に圧縮したが、32byteのヘッダー分を仮に加えた予算値であり、実装済みの公開codecのサイズではない。削除候補はRGS1-Z互換の比較用ファイルの実サイズ。いずれも比較用で、配信へは採用していない。

GTに対するFP16候補の平均指標差は小さかったが、局所的な変化は残った。下位10%削除では人物領域PSNRが平均0.00977dB、最悪の1画像で0.14059dB低下した。平均値だけで同品質と判定しない。全候補で80画像すべてにfloat画素差があり、比較基準モデルでも同様だった。

別に同じwireを再ロードして繰り返す対照を行い、2モデルの160画像すべてでfloat画素が完全一致し、保存24枚のPNGもbyte一致した。この対照では再描画の揺らぎは観測されず、変換後の差はその揺らぎでは説明できなかった。任意の環境での決定性を保証する結果ではない。次の採用判断には、顔・手などの局所変化と連続再生中の見え方の確認が必要になる。
