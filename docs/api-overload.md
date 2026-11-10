# API過負荷時の動作

API入口の共有gateは同時8要求・queue 0。満杯ならbody解析・handler・認証store・生成admissionの前に503を返す。追加goroutine・IP別map・設定flagは持たない。8枠は発表用1 CPU / 512 MiBの保守的な初期値で、実測容量・Cloud Run concurrency 40・10万人への適合値ではない。[Issue #24](https://github.com/Hosi121/rangle/issues/24)の部分対応。

## 対象と応答

| 経路 | gate |
| --- | --- |
| `/api/` のauth/community/generation/catalog | 同じ8枠。generated feed/detailの依存処理も内側 |
| `/internal/generation/sync` | 同じ8枠。OIDC検証前 |
| `/healthz`、`/health`、UI、preview、manifest、asset | 対象外 |

routerより先に判定するため、未登録API・不正methodも飽和中は503になり得る。通過後の認証・CSRF・4096B等のbody制限・429のrate/hash制限・bytes/ETag/Range/cache契約は既存handlerが維持する。HEADは同じstatus/headerでbodyなし。

```http
HTTP/1.1 503 Service Unavailable
Cache-Control: no-store
Retry-After: 1
Content-Type: application/json; charset=utf-8
X-Content-Type-Options: nosniff

{"error":"api_overloaded"}
```

503では依存処理も生成attempt予約も始めない。`Retry-After: 1` は目安の下限で、1秒後の空きを保証しない。呼出し側は回数を有界にしjitterを加える。状態変更を無制限に再送せず、生成createは同じ `request_id` を使う。gateは予約・再送をせず、既存client/schedulerのretryも変更しない。

## 公開交流APIの読み取り頻度

community handlerの `GET` / `HEAD /api/v1/clips/{clip}/{engagement,comments,cheers}` は、同時実行gateとは別に全clip/利用者共通のtoken bucketを使う。

| 項目 | 契約 |
| --- | --- |
| 枠 | 初期burst 20、100msごとに1要求補充（10要求/秒） |
| 判定 | clip存在確認・session照会・Firestoreより先。存在しないclipも消費 |
| 分割 | cookie/query/IPで分けない。`X-Forwarded-For`は解析・信頼しない |
| 超過 | 待機・sleepなし。429、`Retry-After: 1`、`Cache-Control: no-store`、`{"error":"rate_limited"}`。HEADはbodyなし |
| 対象外 | auth・書込・health。既存制限を維持 |

通過後のデータ・鮮度・認証・書込は変えず、cacheを追加しない。cheersはhandler内の登録に適用し、新規経路公開は含まない。10要求/秒・burst20はmax 1の発表構成用の固定初期値で、測定容量ではない。

枠はinstanceごとで再起動によりburstが戻り、別revision/instanceとは共有しない。枠の独占で正規利用者も429になる。1要求の複数document/aggregation課金、Cloud Run要求・media egress、他API、入口DoSを制限せず、課金read数・金額のhard capではない。per-client制限・WAFの代わりにもならない。

## キャンセル・timeout・ログ

handler終了時にdeferで返却し、panicでも解放する。既にcancel済みなら入れない。処理中のcancelはrequest contextで伝え、handlerが戻るまで枠を保持する。先に枠だけ返して依存処理数を増やさない。

gateは別goroutine・timeoutを追加しない。既存contextはcommunity/generated feed各5秒、generation5〜25秒、sync10秒。HTTP header読取・shutdown drainは各5秒。全handlerの終了やslow client排除は保証しない。

起動slogは `api_max_active_requests: 8`、`api_queue_capacity: 0`。飽和WARNはapplication初回1件だけをatomic flagで選び、他の拒否要求をログ待ちさせない。path・cookie・body・IP別状態は保持しない。

## 守る範囲と未達

[Cloud Run](../infra/gcp/modules/presentation/resources.tf)はmin0/max1・concurrency40・1 CPU/512 MiB・timeout30秒を維持。総$300・11月14日までのguard、生成GPU同時1件・attempt10も維持する。HTTP8枠とdurable GPU1枠は別管理。health/static用のCloud Run concurrency/TCP枠を予約せず、CDN/GCS直接配信にも関与しない。複数processでは分散quotaにならない。

局所HTTPテストは503/HEAD/header、依存未到達、cancel/panic解放、cancelを無視する処理の上限を確認する。実applicationではhealth/UI/manifest/Range、復帰後のfeed bytes・ETag/304・CSRF・body制限を確認する。

```sh
go test -race ./...
go vet ./...
```

大規模負荷・単instance容量・水平分散・cold start・region quotaの実割当/他サービス消費・SLO・費用に基づく容量受入は未検証（#24と依存#20/#23）。cloud設定・増設・quota・課金・merge/deployはこの変更に含まない。
