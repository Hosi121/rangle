# LIVE配信の測定：2026-11-09

GPU生成を使わず、既存LIVE metadataと公開済みモデルでAPI・CDNの現状を確認した。APIの小変更はhandler処理を軽くしたが、実HTTPのp99改善は確定していない。CDNのTTL・304・Rangeは確認できた。新モデルへの集中取得、画面表示までの遅延、10万人規模は未検証である。

## 測定条件

- API比較元は`013a798`、変更後の本体は[PR #106](https://github.com/Hosi121/rangle/pull/106)の`5669131`。プロトコル・JSON・cache・認証・8要求の処理枠を維持した。
- ローカルHTTPはLinux/amd64、Go 1.27.3、実serverをCPU11、clientをCPU12へ固定し、それぞれ`GOMAXPROCS=1`。同一マシンのHTTP/1.1 keep-alive、closed-loopである。
- 一覧・session詳細・詳細の条件付きGETを、同時要求1/8/64、各1,000件×3回。1 session・8 segmentsの固定metadataを使った。各版27,000件がすべて期待する200/304となり、transport errorは0だった。
- CDNは手元の単一clientから既存の終了済みsessionを少数取得した。各curlは新しい接続で、DNS/TCP/TLSを含む。cache invalidationや新規モデル公開、大規模な実環境負荷は行っていない。

入力・元動画の復号・GPU学習・画質・ブラウザ描画は今回の測定に含めない。

## API処理と実HTTP

PR #106はsession IDの正規表現を、同じASCII・長さ制約の共通検証関数へ置き換えた。旧正規表現をoracleにして全byte・長さ境界を照合し、90応答のstatus/header/bodyが一致した。cacheのatomic化は、hot loadが約30ns・0 allocationだったため見送った。

handlerの合成fixtureは1 session・1 segment。CPU15、`GOMAXPROCS=1`、変更前後を交互に各300ms×3回測った中央値である。ソケット・TLS・CDNを含まない。

| handler | 前 ns/op | 後 ns/op |
| --- | ---: | ---: |
| session GET | 2,450 | 2,052 |
| session 304 | 1,995 | 1,490 |
| 一覧GET（対照） | 2,010 | 1,887 |
| 一覧304（対照） | 1,512 | 1,434 |

対照にも約5〜6%の変動があり、差の全体をID検証の効果とは扱わない。allocation数は変わらない。維持用benchmarkは`go test ./internal/live -run '^$' -bench 'BenchmarkLive(HTTP|HotLoad)' -benchmem`で実行できる。

実HTTPでは、各1,000件のp99を3回求め、その中央値を比較した。

| 要求 | 同時要求 | 前 p99 ms | 後 p99 ms |
| --- | ---: | ---: | ---: |
| session GET | 1 | 2.002 | 1.796 |
| session GET | 8 | 1.987 | 1.960 |
| session GET | 64 | 10.256 | 9.841 |
| session 304 | 64 | 7.337 | 4.980 |

同時64のGET p99は前7.108〜10.943ms、後7.816〜9.885ms、304も前5.211〜7.419ms、後4.887〜7.743msと重なる。前の全測定→後の全測定という順序で、共有マシン・network stack・接続準備等の変動も含む。実HTTPのp99改善や容量向上を確定した結果ではない。open-loopの到着負荷やCloud Runの容量試験でもない。

別の有限測定では、local indexをatomic replacementしてからAPIに見えるまで1,007/1,003/1,026msだった。observerは20ms間隔でpollした。1秒の共有snapshot cacheを含むmetadata反映時間で、GCS/CDNや映像の表示遅延とは異なる。

## CDNの少数取得

| 対象 | 結果 |
| --- | --- |
| LIVE index 6 GET | 各4,487 bytes、200、`max-age=1`、Age 0 |
| 同indexのIf-None-Match | 304、body 0 bytes |
| scene | 629 bytes、`max-age=86400` |
| model 3 GET | 各5,816,150 bytes、SHA256一致、全量取得1.500/1.867/1.737秒 |
| model Range `bytes=0-31` | 206、32 bytes、`Content-Range`あり |

modelのAgeは約10,464〜10,467秒で、既存cacheからの取得である。cold cache時の値ではない。indexのAge 0だけではcache hitと再検証を区別できない。少数の取得からCDNのp99は求めない。転送完了後の復号・GPUへの転送・最初の描画も別に測る必要がある。

read-onlyで取得した実設定も、indexはTTL/client TTL 1秒、negative caching無効、serve-while-stale 0、request coalescing有効、modelはTTL/client TTL 86,400秒だった。private GCSのため`FORCE_CACHE_ALL`を使う。[キャッシュ仕様](https://docs.cloud.google.com/cdn/docs/caching)

Cloud Run `rangle-stg-00070-r5m`はtraffic 100%、concurrency 40、request timeout 30秒。service-level maxは1、revision-level maxは5であり、通常の有効上限は小さい方の1台である。片方だけを読んで容量を判断しない。[上限の組合せ](https://docs.cloud.google.com/run/docs/configuring/max-instances)

## 次に測ること

1. 既存モデルを一定周期で出す専用producerで、新URLへの集中取得とcache fillを測る。生成速度とは分離する。
2. 更新周期より取得・復号が遅いclientで、中断を繰り返さず新しい描画へ進めるか確認する。
3. capture→公開→index発見→転送→復号→描画を分け、表示中のsource時刻の古さを測る。HTTPのp99だけで低遅延を判定しない。

[PR #105](https://github.com/Hosi121/rangle/pull/105)は、可変indexを長期cacheへ誤配線する更新をGoogleのnative URL map testsで拒否する。TTL・容量を上げる変更ではなく、速度向上の測定対象にも含めていない。クラウドLIVEの入力・学習を含む従来実測は[別記録](cloud-live.md#2026-11-09の実gpu検証)を参照。
