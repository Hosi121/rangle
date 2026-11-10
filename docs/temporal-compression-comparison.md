# 時間付きモデルの圧縮比較

2026-11-09生成の同一259,797点RGS1で、gzip `Z_RLE`は既存Z1 **12,106,161→11,967,872B（138,289B、1.142%減）**。raw14,548,664B比17.739%減。値・順序・点数は保持し、新形式・量子化・間引きは導入しない。

別runの`local-lifetime`は11,626,506B（baseline比3.962%減）だが、学習・点数・圧縮変更を含む。[学習比較](training-quality.md)と圧縮単独の結果を混ぜない。

RLEは既存fixtureで悪化するため、`compress_temporal(payload)`/Node既定は維持。`compress_temporal_smallest(payload)`/`--strategy=auto`は従来とRLEを両方encodeし、小さい方（tieは従来）を選ぶ。RLE単独の速度・メモリ結果はautoに適用できない。

## 固定した比較条件

- T4生成の既存Z1をrawへ一度復号し、全候補へ同じbytesを渡した。
- ローカルWSL2 / Intel Core Ultra 7 255H、Python3.12.3、NumPy2.4.2、zlib1.3。T4時間ではない。
- `process_time`によるCPU時間。encodeはheader検証/転置/gzip/生成、decodeはPythonの検証/gzip/record復元を含み、I/OとSHAを除く。
- 初回7候補＋再計測4回＋逐次RLE9 1回＝12回、約87秒（上限20分）で終了。以後は互換性確認のみ。word-XOR/Brotli/zstdの再探索、研究再現、FP16/間引き評価は含まない。

各1回、時間ms・サイズ10進byte。全候補で元rawの全byte/SHAが一致した。

| gzip設定 | サイズ | encode CPU | decode CPU |
| --- | ---: | ---: | ---: |
| 従来level9 / DEFAULT | 12,106,161 | 1,433.5 | 75.9 |
| level6 / DEFAULT | 12,131,574 | 509.7 | 74.9 |
| level4 / DEFAULT | 12,178,969 | 282.2 | 70.4 |
| level1 / DEFAULT | 12,268,115 | 261.3 | 68.2 |
| level6 / FILTERED | 12,032,827 | 628.4 | 79.3 |
| level9 / FILTERED | 12,017,552 | 1,558.5 | 71.1 |
| level6 / RLE | 11,967,872 | 135.4 | 77.3 |

level低下だけでは増量。RLE9も同サイズでlevel9を維持した。列ごとの逐次RLE9は一括版とencoded bytesも一致し、大きな転置配列とbytesコピーの同時確保を避ける。

peak RSSは入力/Python/NumPy込み。低メモリlauncherの別プロセスで測定し、`/proc/self/status` VmHWMとも照合した（初回launcher継承RSSは不使用）。

| 実装 | encode CPU | decode CPU | encode peak RSS | decode peak RSS |
| --- | ---: | ---: | ---: | ---: |
| 従来DEFAULT、2回 | 1,441.3–1,476.8ms | 75.1–130.3ms | 85.30–85.35MB | 114.54MB |
| 逐次RLE9、1回 | 144.9ms | 78.9ms | 71.28MB | 114.20MB |

RLE単独は従来中央値1,459.0ms比約10.1倍速く、encode peak RSS約16%減。少数標本でdecode/分位点の優位は主張しない。autoの両encode/両出力保持、全学習・公開工程のCPU/RSS/時間は未測定。

## bytesとreaderの互換性

| 内容 | SHA-256 |
| --- | --- |
| 元Z1（従来再encodeとも一致） | `53c82f7ffef6159d16875b702e839199d875a667b244205c96a34db1f6d35ca6` |
| 固定raw RGS1 | `b9a897915a613315734dc5483872641f0c4f4396b7668cb1ba3532f57fd2ce03` |
| Python逐次RLE9 / Node RLE9 | `d8f0c60e84bde7cfe1cc03254ffcd48f32b6947e149e3798a226a6c1555fcc83` |

Python/既存JS `decodeTemporal`で旧/新Z1を復号し、14,548,664Bが一致。autoも同RLEを選択した。record ID/順序、float全bit、初期不可視点を保持。Node v24.13.0/zlib1.3.1-470d3a2 oracleはブラウザ性能測定ではない。

契約は[既存RGS1-Z](gaussian-compression.md#rgs1-zの契約)のまま：`RANGL4Z1`、32byte header、単一gzip、32MiB入力/展開上限、長さ/CRC/終端検証。reader変更・先行配備は不要。HTTP Content-Encodingは付けず、新bytesのSHA/サイズを新revisionへ公開し、既存資産を上書きしない。

この圧縮比較の時点では、正しいZ1の後ろの空gzip memberをPython/native JSが受理し、Goが拒否する差があった。その後[#115](https://github.com/Hosi121/rangle/pull/115)をstgへマージし、実decoderでも単一member契約を強制した。[gzip containerの契約](rgs-gzip-contract.md)に、共通fixture・Chrome 121/146での確認範囲とnative二重復号の追加負荷を記録している。上の通常出力の復号一致を、全入力・全browserの採否一致とは扱わない。コード採用と実環境への配備は別に確認する。

## 明示して使う

Pythonは`compress_temporal_rle(raw)`でRLE単独、`compress_temporal_smallest(raw)`で容量優先。workerのauto exportでは後者を明示する。

```sh
# 従来default
node apps/web/viewer/pack-temporal.mjs raw.rgs output-default.rgs
# 両方式を比較
node apps/web/viewer/pack-temporal.mjs raw.rgs output-auto.rgs --strategy=auto
# RLE単独
node apps/web/viewer/pack-temporal.mjs raw.rgs output-rle.rgs --strategy=rle
```

packerは復号一致後にstrategy/サイズ/SHAをJSON出力。出力既存なら失敗し、圧縮がraw以上ならrawを保持する。

Python契約/追加テスト、Node oracle、JS codec/packerテストで、RLE悪化、auto選択/tie、旧Z1/D1、破損/切断/余分なgzip/宣言サイズ不整合を確認した。CPU比較はWebGL・実ブラウザ・スマートフォンの性能や学習品質を測らず、export前のRGB/alpha/quaternion量子化も取り消さない。

## FP16精度候補の評価（未採用）

同じ`local-lifetime`を追加学習せず80 held-out画像で比較。原wireは250,637点、raw14,035,704B、auto11,626,506B。12floatすべて、またはposition3floatを除く9floatをFP16往復し、既存float32欄へ戻して同autoで圧縮した。RGB8/quaternion8、順序/数、時刻別RGBA8評価は共通。rawサイズは同じで、新FP16形式の実装・配備ではない。

| 表現 | 圧縮bytes | GT PSNR (dB) | GT SSIM | 原wire比平均MAE | 最大絶対RGB差 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 原wire | 11,626,506 | 27.814041 | 0.915371 | 0 | 0 |
| 全float FP16往復 | 6,818,929 | 27.798317 | 0.915068 | 0.001186555 | 0.351430 |
| position FP32・他FP16往復 | 8,023,765 | 27.813830 | 0.915368 | 0.000056239 | 0.237637 |

WebP保存前のgsplat/RGB`[0,1]`評価。MAEは画像平均の80枚平均、maxは全pixel/成分の最大。max位置は全float：frame0/view30、position保持：frame19/view30。平均が近くても局所差は残る。

同一モデル/80画像の1回、同T4 job内18.100秒、追加GPU学習jobなし。WebGL/スマートフォンの差・速度は未測定。最初/中間/最後のWebPはGT→原wire→候補順。**FP16未採用、配信は原wireのみ。候補モデルは保存・公開していない。**
