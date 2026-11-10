# RGS1-Z gzip契約

`RANGL4Z1`の32byteヘッダー後には、gzip memberを1個だけ格納する。追加member、NUL padding、末尾の余剰byteは拒否する。

## 受け入れ条件

- 入力・復号後とも32MiB以内、Gaussian数は1〜500,000。展開payloadは正確に`count × 56` bytesとする。
- gzipヘッダー・CRC32・ISIZE・FHCRCの不正、途中終端、長さ不一致を拒否する。
- 復号後の全bytes、record順、float32値を保持する。

## 実装

Goは単一memberに限定した`gzip.Reader`、Pythonは終了位置を確認できる`zlib`を使う。JSはnative gzipで復号し、zlibで境界を再検証する。実装ごとの末尾許容の差を、この2-passで吸収する。

## 検証と負荷

[共通fixture R01–R38](../testdata/contracts/v1/README.md)でGo/Python/JSの採否と復号bytesを確認する。Chrome 121・146でも同じケースの一致を確認済み。

JSは二重復号のためCPU負荷が増える。Chrome 146・12.1MB資産の復号中央値は、gzipのみ84.3ms、現方式177.6ms。スマートフォンの性能は未確認。

測定条件と採用理由は[PR #115](https://github.com/Hosi121/rangle/pull/115)、gzipの要件は[Compression Standard](https://compression.spec.whatwg.org/#supported-formats)を参照。
