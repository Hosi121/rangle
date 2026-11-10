# RGS1-Z gzip container の契約

`RANGL4Z1` の32byteヘッダー後は、**正確に1個のgzip member** とする。既存のpackerはこの形式を出力する。空memberを含む追加member、先頭や末尾のNUL padding、その他の余剰byteは受理しない。複数memberの展開結果が同じRGS1になる場合も拒否する。

[RFC 1952](https://www.rfc-editor.org/rfc/rfc1952#section-2.2) の一般gzip形式は複数memberを許すが、これは資産内部のより狭い契約である。[WHATWG Compression Standard](https://compression.spec.whatwg.org/#supported-formats) のgzipも単一memberと末尾余剰の拒否を規定する。ただしNode 24.13.0の実`DecompressionStream('gzip')`は追加memberを受理するため、constructorが存在するだけで境界の一致を仮定しない。

## 検証する内容

- wireと復号後は32MiB以内、primitiveは1〜500000、展開payloadは正確に`count * 56` bytes。32MiBはプロセス全体のRAM上限ではない。
- 圧縮方式はDEFLATE（CM=8）。予約flagはゼロ。FTEXTは無視し、FEXTRA/FNAME/FCOMMENTは正しく終端する場合に受理する。
- CRC32・ISIZEを検証し、FHCRCがある場合も検証する。32MiB未満なので、単一memberのISIZEと宣言展開長はmoduloの曖昧さなく一致する。
- 展開長の偽装や途中終端、追加member、NUL paddingを含む余剰を拒否する。
- 復号後の全byte、row順、float32値は変えない。既存のprimitive・timeline・SHA/byte数の検証を各信頼境界で維持する。

## 各実decoder

| 境界 | 実装 |
| --- | --- |
| Go publication / generation | native `gzip.Reader`を`Multistream(false)`で読む。`count * 56 + 1`の上限、EOFまでのCRC、残り圧縮byteゼロを確認。Goが黙認する予約flagはenvelope側でも検査する |
| Python packing | native `zlib.decompressobj(wbits=31)`を展開長+1で制限し、`eof`、`unused_data`、`unconsumed_tail`を確認。`GzipFile`の追加member・NUL padding許容に依存しない |
| JS Worker | native gzipでCRC・ISIZE・FHCRCと展開総長を検証し、直接AoSへ戻す。次に同じdeflate bodyをzlib envelopeへ包み、native `deflate`で境界を再検証する |

JSはgzip headerのoptional fieldをたどってbodyの範囲を求めるが、Deflate blockやHuffman符号を解析しない。第1passのAoS書き戻しと同じループで[RFC 1950](https://www.rfc-editor.org/rfc/rfc1950#section-8) のAdler-32を計算し、`78 01`のzlib headerとchecksumをbodyへ付ける。和はlocal変数へ保持し、最大5552 bytesごとにmoduloを取る。単一memberなら有効なzlib streamになる。追加memberや余剰があれば、第1memberの終了後にgzip trailer等が残るため、native zlibのchecksumまたは末尾検証が失敗する。raw `deflate-raw`への置換は、Nodeで末尾を黙認したため使わない。

第2passは出力を保存せずdrainし、同じ展開総長上限を適用する。全文の第2 decoded bufferは作らない。圧縮bodyには元Blobのsliceを再利用し、第2の全文Uint8Arrayを作らない。ただしnativeの内部copyやGCまで含むRAM上限を保証するものではない。

両passはエラー時にreaderをcancelし、常にlockを解放する。Workerのabort signalを両passへ渡し、非表示・破棄時のキャンセルを継続する。timeout付きsignalも同じ経路で処理する。native decoderが1回の処理内で使うCPUへ強制的な時間上限を設けたわけではない。

## 選択肢と負担

| 案 | 判断 |
| --- | --- |
| 一般gzipの複数memberを受理 | Go/Pythonでは容易だが、WHATWGの単一member契約、native JSの差、追加空member・paddingの差が残るため採らない |
| 独自Deflate parserで境界を求める | 圧縮blockとHuffmanを保守する負担が大きく、採らない |
| 小さなinflate依存を追加 | [fflateの実装](https://github.com/101arrowz/fflate/blob/master/src/index.ts)は`Gunzip.onmember`を持つが、gunzipのCRC検証を提供しない。境界通知だけで現在のnative CRC検証を置換できず、別の検証と依存管理が必要になるため、今回は追加しない |
| native gzip + native zlib境界検証 | 追加依存と独自Deflateを避けられるが、二重復号でCPUと一時メモリが増える。以下の負担を伴う実装としてレビューする |

空memberを追加した場合はISIZEを使って早期拒否する。先頭の空member列はnative gzipが処理してから境界で拒否する場合があり、member数の独立上限は設けていない。入力32MiB・展開総長の上限を維持し、反復空memberもfixtureで確認する。

## 同じ実資産での局所比較

既存の非公開RGS1-Z資産（259797 primitives、20 frames、wire 12106161 bytes、raw 14548664 bytes）を使い、初期の2-pass実装を同一端末・Node 24.13.0・別プロセスで比較した。各方式を1回暖機して3回測定し、復号後SHAが同一であることを確認した。画像・GPU・ネットワークは使用していない。次のNode値はループ統合前の記録である。

| 指標 | 旧native gzipのみ | 新native 2-pass |
| --- | ---: | ---: |
| 復号ms、3標本 | 105.13 / 100.78 / 142.14 | 175.87 / 222.50 / 169.49 |
| 復号中央値ms | 105.13 | 175.87 |
| 読み取り境界でsampleしたArrayBuffer最大bytes | 76181011 | 90253224 |
| プロセスmaxRSS、KiB | 154128 | 163844 |

新方式はこの測定で中央値が約67%増えた。高速化や端末全体の省メモリ化を示す変更ではない。第2Blobへ渡す新しいBufferSourceはheader/checksumの6bytesだけだが、native chunkの確保とGCにより観測メモリも増えた。メモリ値は同じ入力量を読んだ後・GC後を基準に別の1回でsampleし、maxRSSはプロセス全体の記述値とした。完全なpeak測定ではない。

2026-11-10の統合前確認では、Chrome 146.0.7680.76の独立したブラウザcontextで同じ実資産を比較した。各方式を1回暖機＋5回、旧→新→新→旧の順に測定し、方式ごとの10標本から中央値を取った。取得とbytes比較は計時外、ブラウザのmain threadでdecoderだけを測り、アプリ画面やGPUは操作していない。

| Chrome 146の復号中央値 | gzipのみ | 2-pass |
| --- | ---: | ---: |
| ループ統合前 | 86.1ms | 225.3ms |
| AoS書き戻し・Adler計算を統合後 | 84.3ms | 177.6ms |

この局所測定では2-pass内の負荷を約21%減らしたが、従来のgzipだけの経路より重い。追加依存や独自Deflate parserを増やさず、単一memberの境界を検証するための負担として採用する。各回とも復号後全bytesを照合し、SHA-256は `b9a897915a613315734dc5483872641f0c4f4396b7668cb1ba3532f57fd2ce03` で一致した。

Chrome 121.0.6167.85と146.0.7680.76で、RGS38ケースの採否と正例の復号bytesも一致した。121の実行は回帰テストと重なったため性能比較に使わない。これらの数値をスマートフォン、アプリ全体・本番Workerの待ち時間・初回GPU表示時間・端末のpeak memoryへ外挿しない。

## 共通fixtureと確認範囲

[v1 corpus](../testdata/contracts/v1/README.md) のR01–R38を、Goのpublication/generation、Pythonの実packing、JSの`decodeTemporal → prepareTemporal`へ通す。R15–R19/R32は追加・分割・反復member、R20/R21はpadding、R22–R28/R34/R37はCRC/header/終端、R30/R36/R38はwire/展開上限を固定する。正しいoptional header、magicに見えるbody、primitive検証も含む。

これは74個の列挙済み合成fixtureとNode native経路、およびChromeのRGS38ケースでの確認である。すべてのDeflate表現・全browserに対する採否の完全等価性を証明するものではない。packerの通常出力と、その全byte可逆性は既存oracleでも継続して確認する。
