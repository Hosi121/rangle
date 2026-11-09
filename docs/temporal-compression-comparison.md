# 実T4モデルのZ1圧縮比較

2026-11-09に生成した259,797 Gaussianの同じRGS1を、値・順序・数を変えずに比較した。今回のモデルではgzipの `Z_RLE` により、既存Z1の12,106,161Bから11,967,872Bへ138,289B（1.142%）減った。raw 14,548,664Bからの削減は17.739%。新形式・量子化・間引きは導入していない。

RLEは辞書圧縮が得意な既存fixtureでは悪化したため、既存 `compress_temporal(payload)` とNode packerの既定は変更しない。追加の `compress_temporal_smallest(payload)` / `--strategy=auto` は従来方式とRLEを両方encodeして小さい方を選び、同じサイズなら従来方式を保つ。従来Z1より容量を増やさず適用できるが、RLE単独の速度・メモリの測定値をautoや学習全体の改善として扱わない。

## 固定した比較条件

- 入力はT4で生成した同一モデルの既存Z1。元の固定float RGS1へ一度復号して全候補へ同じbytesを渡した。
- CPUはローカルWSL2 / Intel Core Ultra 7 255H。Python 3.12.3、NumPy 2.4.2、zlib 1.3。T4でのencode/decode時間ではない。
- encodeはheader検証・列転置・gzip・結果生成、decodeは既存Python decoderによるheader検証・gzip・元のrecord配置への復元を含む。`process_time`でプロセスのCPU時間を測り、ファイルI/O・SHA計算は時間の外に置いた。
- 初回7候補、従来方式とRLE9の再計測4回、RLE9の逐次encode 1回の計12回で探索を終了。比較開始から終了までは約87秒で、20分の上限内。以後はcodec互換性の検証だけを行った。
- 過去に比較済みのword-XOR / Brotli / zstdを再探索していない。新しい研究の再現や、FP16・間引きの評価でもない。

初回の候補比較は各1回。時間はms、サイズは10進byte。全候補で復号後の全byteとSHA-256が元RGS1に一致した。

| gzip設定 | サイズ | encode CPU | decode CPU |
| --- | ---: | ---: | ---: |
| 従来level9 / DEFAULT | 12,106,161 | 1,433.5 | 75.9 |
| level6 / DEFAULT | 12,131,574 | 509.7 | 74.9 |
| level4 / DEFAULT | 12,178,969 | 282.2 | 70.4 |
| level1 / DEFAULT | 12,268,115 | 261.3 | 68.2 |
| level6 / FILTERED | 12,032,827 | 628.4 | 79.3 |
| level9 / FILTERED | 12,017,552 | 1,558.5 | 71.1 |
| level6 / RLE | 11,967,872 | 135.4 | 77.3 |

levelを下げるだけでは容量が増えた。RLE9は同じサイズとなり、既存のlevel9指定を保った。RLE9の列ごとの逐次encodeは、全列をまとめたRLE9とencoded bytesも一致した。逐次化は最大の転置配列とbytesコピーを同時に作ることを避ける。

peak RSSは入力読込とPython/NumPyを含むプロセス全体の最大常駐量。低メモリのlauncherから別プロセスを起動して測り、`/proc/self/status` のVmHWMとも照合した。初回launcherから継承したRSS値は比較に使わない。

| 実装 | encode CPU | decode CPU | encode peak RSS | decode peak RSS |
| --- | ---: | ---: | ---: | ---: |
| 従来DEFAULT、2回 | 1,441.3–1,476.8ms | 75.1–130.3ms | 85.30–85.35MB | 114.54MB |
| 逐次RLE9、1回 | 144.9ms | 78.9ms | 71.28MB | 114.20MB |

RLE単独のencodeは従来2回の中央値1,459.0msに対して約10.1倍速く、encode peak RSSは約16%少なかった。標本数は少なく、decode時間の差や分位点の優位は主張しない。autoは両encodeと両出力の保持が必要で、この10.1倍・16%削減を適用できない。autoのCPU/peak RSSと、学習・公開工程全体の時間は今回測っていない。

## bytesとreaderの互換性

| 内容 | SHA-256 |
| --- | --- |
| 元のZ1（従来方式の再encodeとも一致） | `53c82f7ffef6159d16875b702e839199d875a667b244205c96a34db1f6d35ca6` |
| 固定したraw RGS1 | `b9a897915a613315734dc5483872641f0c4f4396b7668cb1ba3532f57fd2ce03` |
| Python逐次RLE9 / Node RLE9 | `d8f0c60e84bde7cfe1cc03254ffcd48f32b6947e149e3798a226a6c1555fcc83` |

実モデルの従来Z1とPython/Nodeの新RLE Z1を、既存JS `decodeTemporal` とPythonで復号し、14,548,664Bの全byte一致を確認した。autoも同じRLE bytesを選んだ。canonical IDとなるrecord順序、floatの全bit、初期時刻に不可視のGaussianも保持する。Node v24.13.0 / zlib 1.3.1-470d3a2のoracleはブラウザの再生・復号時間を測るものではない。

magicは引き続き `RANGL4Z1`、32byte headerの残りも同一、payloadは単一gzip member。既存D1/Z1のreaderを変更せず読めるため、reader先行配備は不要。32MiBの入力・展開上限、宣言された出力長、CRC・終端検証も変更しない。HTTPの `Content-Encoding` は付けず、新しいbytesは新しいrevisionへ公開する。圧縮後のSHA・サイズをmanifestへ記録し、既存公開資産を上書きしない。

## 明示して使う

Pythonでは従来のimportとcallをそのまま使える。追加の `compress_temporal_rle(raw)` はRLE単独、`compress_temporal_smallest(raw)` は容量優先の選択で、いずれも同形式Z1を返す。学習workerでautoを使う際はexport側で後者を明示する。

```sh
# 従来のdefault。既存の呼出しは変更不要。
node apps/web/viewer/pack-temporal.mjs raw.rgs output-default.rgs
# 容量を比較して選ぶ。入力rawより大きければ従来どおりrawを保持する。
node apps/web/viewer/pack-temporal.mjs raw.rgs output-auto.rgs --strategy=auto
# 測定済み資産でRLEだけを使う場合。
node apps/web/viewer/pack-temporal.mjs raw.rgs output-rle.rgs --strategy=rle
```

packerは復号後の全byte一致を確認し、選択したstrategy・サイズ・SHAをJSONへ出力する。出力先が存在すれば失敗する。autoのtieは従来方式、rawと同サイズの場合は既存動作どおりrawとなる。

検証はPythonの既存契約と追加テスト、Node oracle、JS codec/packerテストで行った。RLE単独が悪化する入力、autoの最小選択・tie、旧Z1/D1、破損・切断・余分なgzip bytes、宣言サイズの不整合を含む。学習画像品質、WebGL、スマートフォン、実ブラウザのCPU・memoryは測定していない。全byte一致は固定した配信用RGS1に対するものであり、RGS1に書く前の既存RGB/alpha/quaternion量子化の影響を消すものではない。
