# LIVE配信の測定：2026-11-09

既存metadata/公開モデルを使い、GPU生成なしで測定した。handlerは軽くなったが実HTTPのp99改善は未確定。集中取得・描画遅延・10万CCUは未検証。

## 測定条件

- APIは`013a798`と[PR106](https://github.com/Hosi121/rangle/pull/106)の`5669131`を比較。protocol/JSON/cache/認証/同時8処理枠を維持。
- Linux/amd64、Go1.27.3、server CPU11/client CPU12、各GOMAXPROCS=1。同一PC、HTTP/1.1 keep-alive、closed-loop。
- 1 session/8 segments、同時1/8/64、各1,000件×3回。各版27,000件が期待200/304、transport error0。
- CDNは単一clientの少数取得。curlは毎回新接続でDNS/TCP/TLS込み。新モデル公開・invalidation・大量負荷は行わない。

入力・元動画復号・GPU学習・画質・ブラウザ描画は対象外。

## API処理と実HTTP

ID正規表現を同じASCII/長さ制約の関数へ置換。旧式をoracleに全byte・長さ境界と90応答のstatus/header/body一致を確認した。hot load約30ns/0 allocationのためcache atomic化は見送った。

handlerは合成1 session/1 segment、CPU15/GOMAXPROCS=1、前後交替300ms×3回の中央値（ソケット/TLS/CDNなし）。

| handler | 前 ns/op | 後 ns/op |
| --- | ---: | ---: |
| session GET | 2,450 | 2,052 |
| session 304 | 1,995 | 1,490 |
| 一覧GET（対照） | 2,010 | 1,887 |
| 一覧304（対照） | 1,512 | 1,434 |


対照にも5〜6%変動があり全差を置換効果とはしない。allocation不変。再測定：`go test ./internal/live -run '^$' -bench 'BenchmarkLive(HTTP|HotLoad)' -benchmem`。

実HTTPは1,000要求のp99を3回求めた中央値。

| 要求 | 同時要求 | 前 p99 ms | 後 p99 ms |
| --- | ---: | ---: | ---: |
| session GET | 1 | 2.002 | 1.796 |
| session GET | 8 | 1.987 | 1.960 |
| session GET | 64 | 10.256 | 9.841 |
| session 304 | 64 | 7.337 | 4.980 |


同時64 GETは前7.108〜10.943/後7.816〜9.885ms、304は前5.211〜7.419/後4.887〜7.743msで重なる。前を全測定→後を全測定したため、共有PC/network/接続準備の変動を含む。p99/容量改善やopen-loop/Cloud Run容量の実証ではない。

local indexのatomic replacement→API反映は1,007/1,003/1,026ms（observer20ms poll）。1秒snapshot cache込みのmetadata反映で、GCS/CDN・映像表示遅延とは別。

## CDNの少数取得

| 対象 | 結果 |
| --- | --- |
| LIVE index 6 GET | 各4,487 bytes、200、`max-age=1`、Age 0 |
| 同indexのIf-None-Match | 304、body 0 bytes |
| scene | 629 bytes、`max-age=86400` |
| model 3 GET | 各5,816,150 bytes、SHA256一致、全量取得1.500/1.867/1.737秒 |
| model Range `bytes=0-31` | 206、32 bytes、`Content-Range`あり |


modelはAge約10,464〜10,467秒のwarm cache。index Age0だけでhit/再検証は区別できず、少数sampleからCDN p99は求めない。復号/GPU転送/描画は未計測。

当時の実設定はindex TTL/client TTL1秒、model86,400秒、negative caching無効、serve-while-stale0、coalescing有効、private GCS向け`FORCE_CACHE_ALL`。[cache仕様](https://docs.cloud.google.com/cdn/docs/caching)

Run `rangle-stg-00070-r5m`はtraffic100%、concurrency40、timeout30秒。service max1/revision max5なので有効上限は小さい方の1。[上限の組合せ](https://docs.cloud.google.com/run/docs/configuring/max-instances)

## 次に測ること

1. 既存modelの専用producerで新URLへの集中取得/cache fillを測り、生成速度と分離する。
2. 更新周期より遅いclientで、中断を繰り返さず新しい描画へ進めるか測る。
3. capture→公開→index発見→転送→復号→描画を分け、表示source時刻の古さを測る。

[PR105](https://github.com/Hosi121/rangle/pull/105)のnative URL map testsは誤ったcache配線の防止で、速度改善の測定対象外。[GPU生成込みの測定](cloud-live.md#2026-11-09の実gpu検証)は別記録。
