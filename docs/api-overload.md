# API過負荷時の動作

API入口へ共有8枠の同時実行gateを置き、依存先へ進むhandler数を有界にする。8枠が埋まっている要求は待たせず、handler・認証store・生成admissionへ渡す前に503を返す。待ち行列、追加goroutine、IP別map、新しい設定flagは持たない。

8は、発表用の1 CPU / 512 MiBプロセスに置く保守的な初期上限である。実測した最適値・処理能力ではなく、Cloud Run concurrency 40や10万視聴者に適合すると判断した値でもない。[Issue #24](https://github.com/Hosi121/rangle/issues/24)の部分対応として扱う。

## 対象と応答

| 経路 | gate |
| --- | --- |
| `/api/` 配下のauth・community・generation・catalog | 同じ8枠を共有 |
| generated feed/detailの依存先参照 | 上記API枠の内側 |
| `/internal/generation/sync` | 同じ8枠を共有。OIDC検証前に判定 |
| `/healthz`、`/health`、UI、preview、manifest、asset | 対象外 |

API prefixでrouterより先に判定するため、未登録のAPIや無効なmethodも飽和中は503になり得る。空きがある要求は既存handlerへそのまま渡す。既存の認証、CSRF、4096B等のbody制限、429のrate/hash制限、応答bytes、ETag・conditional GET・Range・cache方針は各handlerが維持する。

超過時の応答は次の通り。HEADは同じstatus/headerを返し、bodyを出さない。

```http
HTTP/1.1 503 Service Unavailable
Cache-Control: no-store
Retry-After: 1
Content-Type: application/json; charset=utf-8
X-Content-Type-Options: nosniff

{"error":"api_overloaded"}
```

この503は依存処理を開始しておらず、生成attemptを予約せず、bodyも解析しない。`Retry-After: 1` は再試行までの下限の目安であり、1秒後の空きを保証しない。呼出し側は回数を有界にし、待ち時間へjitterを加える。状態変更要求を無制限に再送せず、生成createの再試行では同じ `request_id` を保つ。gate自身は再送も予約も行わない。既存clientやschedulerのretry実装をこの変更で追加・変更したものではない。

## キャンセル・timeout・ログ

枠はhandlerが終了するとき、deferで返す。clientのキャンセルを既存のrequest contextで依存先へ伝え、contextを尊重するhandlerが戻れば枠も解放される。キャンセルを無視して処理を続けるhandlerには、終了まで枠を持たせる。contextがキャンセルされた瞬間に枠だけを先に返すと、実行中の依存処理数を上限以上へ増やせるためである。panicで戻る場合も枠を返す。既にキャンセル済みの要求はhandlerへ入れない。

gateはhandlerを別goroutineへ移さず、新しいtimeoutを設定しない。既存のcommunity 5秒、generated feed参照5秒、generation各処理5〜25秒、sync 10秒のcontextと、HTTP header読取5秒・shutdown drain 5秒を維持する。これは全handlerの終了やslow clientの排除を保証する仕組みではない。

起動時のslogへ `api_max_active_requests: 8`、`api_queue_capacity: 0` を出す。飽和のWARNはapplicationごとに初回の1件だけとし、要求ごとのWARN/ERROR増幅を避ける。要求path・cookie・bodyやIP別状態はログ用に保持しない。単一のatomic flagで初回を選ぶため、他の拒否要求がそのログ出力の完了待ちへ並ぶこともない。

## 守る範囲と未達

[発表用Cloud Run設定](../infra/gcp/modules/presentation/resources.tf)はmin 0 / max 1、concurrency 40、1 CPU / 512 MiB、request timeout 30秒のまま。総$300・11月14日までの既存guard、生成の同時GPU job 1・attempt上限10を変更しない。APIの8枠はHTTP handlerの枠であり、GPU admissionは従来のdurableな1枠で管理する。

health/staticをgate対象外とすることは、Cloud Run側のconcurrencyやTCP接続枠を予約することではない。CDN/GCSへの直接配信にはこのgateは関与しない。プロセスを増やした場合は各プロセスが独立したgateを持ち、分散quotaとしては働かない。

局所Concurrent HTTPテストで、上限中の503/HEAD/header、依存処理への未到達、cancel後の枠解放、cancelを無視する処理の上限維持、panic時の解放を確認する。実applicationでもhealth/UI/manifest/Rangeを利用でき、空き復帰後にfeed bytes・ETag/304・CSRF・body制限が保たれることを確認する。

```sh
go test -race ./...
go vet ./...
```

大規模負荷、単instance容量、水平分散、cold start、region quotaの実割当・他サービス消費、SLO、費用からの容量設定受入は未検証のまま。これらはIssue #24と依存する#20/#23の残件であり、この安全弁だけで完了とはしない。今回の変更は実cloud設定、増設、quota、課金、merge、deployを実行しない。
