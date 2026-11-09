# 発表用 stg の配備

対象は `comin-up`（project number `113704194486`）、Tokyo `asia-northeast1`。承認済み総予算は USD300、停止期限は **2026-11-14 15:00 UTC / 11月15日00:00 JST**。Cloud Run＋Cloud CDNを配備し、`app.rangleplay.stream` と `media.rangleplay.stream` へ接続している。Firebase は使用しない。10万同時視聴の容量実証とは区別する。

## 配信と初回配備の入力

`base_domain` は nullable で既定 `null`。実ドメインを推測した値や仮のホスト名を実設定へ入れない。確定後は `app.<base_domain>` と `media.<base_domain>` を使う。

- `null` の間は、private GCS の CDN backend bucket、cache-fill IAM、専用 global IPv4 `rangle-stg-edge` まで準備できる。certificate、proxy、forwarding rule、HTTP/HTTPS URL map、アプリはまだ作らない。予約 IP 自体の費用は発生し得る。
- ドメイン設定後は、Google-managed certificate と外部 global Application Load Balancer を作る。app と media の hostname を URL map で分離する。`app` は CDN 無効の serverless NEG / Cloud Run、`media` だけは CDN backend bucket。API/UI/catalog/入力を CDN bucket に混ぜない。
- Go＋Flutter Web は Cloud Run `rangle-stg` に置く。1vCPU/512MiB、request billing、min0/max1、concurrency40、timeout30秒。`/` landing、`/demo/*`、root API の path を書き換えない。`-asset-origin` は `https://media.<base_domain>` に固定する。
- **コード上のingressは `internal-and-cloud-load-balancing`。** インターネットからのUI/API要求はLBを通し、既定 `run.app` / candidate-tag URLの直通は拒否する。既定URI自体はGoogle管理callerとの互換性のため維持する。Goの認証・CSRF・管理者/所有者認可と、generic main / platform rootは維持する。[Cloud Run ingress](https://docs.cloud.google.com/run/docs/securing/ingress)

`deployment` output に `edge_ip`、`base_domain`、`app_origin`、`media_origin`、既定 `app_uri`、DNS A record 2件を返す。Cloudflare には app / media の A record を同じ IP へ追加し、**DNS only（proxy 無効）**とする。矛盾する A/AAAA がないことを確認し、Google certificate が ACTIVE になるまで待つ。ここに Cloudflare provider・API token・DNS 自動変更は含めない。[Google-managed TLS](https://docs.cloud.google.com/load-balancing/docs/ssl-certificates/google-managed-certs)

## private GCS と公開工程

既存の catalog / inputs / media / state bucket と runtime / deploy SA、WIF、専用予算を維持する。media bucket は `rangle-stg-113704194486-media` をそのまま private CDN origin として使う。全 bucket は PAP enforced で、`allUsers` を付けない。backend bucket 作成後に生成される `service-113704194486@https-lb.iam.gserviceaccount.com` に media bucket の objectViewer を付与する。これが、署名 URL なしで private GCS origin から公開 CDN 再生するための接続である。[private bucket access](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket#make_your_cloud_storage_bucket_private)、[cache-fill IAM](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket#make_your_cloud_storage_bucket_private)

media は公開許諾済みの不変 bytes だけ。CDN は `FORCE_CACHE_ALL`、default/client TTL86400秒（APIがこのmodeで省略するmax TTLは設定しない）、negative cache / stale serve / dynamic compression 無効。query は `generation` だけを cache key に残し、content selection や認可に query を流用しない。`Origin` は cache key の明示指定がAPIで禁止されているため、GCS の `Vary: Origin` を CDN が扱う仕組みを使う。匿名 public media の CORS は `*`、GET/HEAD と Range 応答に必要な header を設定する。credentials 付き取得は使わない。実 CDN の GET/HEAD/206/304/416・CORS・hash・cold miss は配備後に確認する。[CDN cache](https://docs.cloud.google.com/cdn/docs/caching)

catalog は private bucket から `/mnt/catalog/snapshots/<sha256>.json` へ readonly mount し、runtime の追加権限はその bucket の objectViewer のみ。FUSE の制約から catalog だけ versioning 無効。publisher は allowlist・許諾・bytes/hash/header を検証した asset → manifest → 最後に catalog の順で新 key へ配置する。preview / scene は確定した media origin の絶対 URL とし、Go が存在しないローカル UI ファイルとして pin しないようにする。ドメイン決定後の metadata を含めて catalog を再計算する。Terraform は asset を upload しない。[GCS mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)、[FUSE 制約](https://docs.cloud.google.com/storage/docs/cloud-storage-fuse/overview#limitations)

## Terraform と CI

Terraform 1.17.1、Google provider 8.10.0 と lock file を固定。presentation の google-beta / Firebase 定義は除去する。既に作られた guard の Hosting Admin は、その IAM member binding だけを削除する。Firebase API の管理記録を外しても `disable_on_destroy=false` により API を無効化しない。他の project resource を import / 削除しない。

`presentation-bootstrap` の inputs は `project_id`、`region`、`state_bucket_name`。state bucket `rangle-stg-113704194486-tfstate` と backend prefix `rangle/presentation-stg` は既存のまま。認証は実行元 ENV の短命 `GOOGLE_OAUTH_ACCESS_TOKEN` で、token / tfvars / plan / state を公開しない。SA key や ADC を前提にしない。既存 AR `rangle-stg` は data 参照だけで設定や image を変更しない。

環境 root の必須値は `project_id`、`project_number`、`region`、`budget_currency`、`budget_stop_amount`。初期 `base_domain=null` / `application=null`。実ドメイン、検証済み image digest と実 catalog SHA256 が揃ってからアプリを追加する。`application` / optional `budget` の形は variables.tf と fake tfvars example に記録する。

CI は image と traffic を所有し、Terraform は両者を ignore する。runtime args / mount / catalog / scale / IAM は IaC が保つ。catalog / runtime 設定を変える前は現行の正常 revision へ traffic を明示固定し、apply でできた revision は smoke 後に昇格する。IaC apply が CI の rollback を LATEST100 で戻す運用にしない。

### LB入口制限への切替

CI候補用に同じCloud Run serviceを指すserverless NEGとCDN無効のbackendを追加する。NEGはCI専用の固定tag `ci-candidate` を指し、app hostnameへの `X-Rangle-Candidate: ci-candidate` 付き要求だけをそこへ送る。通常の要求・Schedulerは既存backendへ送る。新しいDNS、証明書、サービス、CIのnetwork/IAM変更権限は追加しない。headerは公開のルーティング指定で、認証用の秘密ではない。候補にもGoの通常の操作認可が適用される。[NEGのtag指定](https://docs.cloud.google.com/sdk/gcloud/reference/compute/network-endpoint-groups/create)、[header routing](https://docs.cloud.google.com/load-balancing/docs/https/traffic-management-global)

1. 現行の正常revisionとtraffic比率を記録し、LATEST参照を使わず明示固定する。固定tag `ci-candidate` はこの直列deploy workflowだけが使う。初回のNEG作成前には正常revisionへこのtagを割り当てる。
2. presentation rootでplanを確認し、候補NEG/backend、app hostのrouting、Run ingressの変更を適用する。期限・予算停止、Scheduler、private bucket/IAM、mediaのrouting/cacheに不要な変更がないことを確認する。既存のimage/traffic所有権は変えない。
3. `stg` workflowを実行する。旧ALL ingressのままではimage更新前に停止する。既定URLと候補tag URLの `/health` が外部から404となることを確認し、候補・昇格後のHTTP検証はappのLB URLで行う。
4. CIはHTTPの `X-Rangle-Revision` とcatalog SHA、実revisionのimage/argsを照合する。新候補は `K_REVISION` を返すimageが必要。tag反映待ちは最大5回に制限し、検証失敗なら昇格しない。昇格後の失敗は記録したtraffic比率へ戻す。終了時は専用tagだけを削除する。
5. SchedulerのOIDC同期をLB経由で確認する。audience・専用SA・Goのtoken検証は変更しない。期限・予算停止の設定とreceiptも維持する。

本変更の実環境適用・外部直通拒否・LB候補経路・Schedulerの再疎通は未実施。以下の既存配備記録とは区別する。ネットワーク境界は外部入口の制限で、専用VPC/subnetによる隔離を追加する変更ではない。

| 項目 | 値 |
| --- | --- |
| image base | `asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server` |
| deploy SA | `rangle-stg-deploy@comin-up.iam.gserviceaccount.com` |
| WIF provider | `projects/113704194486/locations/global/workloadIdentityPools/rangle-stg-github/providers/deploy-stg` |
| GitHub repository / owner ID | `1448803636` / `128687243` |
| ref / workflow | `refs/heads/stg` / `Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg` |
| events | `push`、`workflow_dispatch` |

OIDC は標準 `workflow_ref` を使う。WorkloadIdentityUser、runtime actAs、Run developer と CI の project read-only `run.operations.get` に期限条件を付ける。実適用で AR repository の条件付き policy が拒否されたため、writer は専用 repo のみに条件なしで付け、WIF の期限条件と guard による deploy SA disable で制限する。CI に Terraform apply、network admin、IAM 変更、Billing 操作は与えない。[WIF pipeline](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)

## 予算・期限の停止

既存の **Cloud Run 200円 spend cap は変更・解除しない**。Cloud Run が先に停止する可能性がある。この cap は CDN / LB / GCS / AR を止めない。[Spend cap の範囲](https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps)

専用 budget は全 service、単一 project、`ownership_scope` 未指定（API既定は `ALL_USERS` と同義）、custom period `2026-11-08`〜`2026-11-15`、credits 除外、JPY30000、threshold25/50/75%、project owner 通知、既定 billing-account recipient 無効。account data source は参照しない。通知先は `projects/comin-up/topics/rangle-stg-budget`。公式 publisher `billing-budget-alert@system.gserviceaccount.com` にこの topic だけの Publisher を付ける。[公式 principal](https://docs.cloud.google.com/organization-policy/restrict-domains)

15分ごとに Scheduler が OAuth で Workflow を実行し、専用 subscription `rangle-stg-budget-guard` の display name `rangle-stg-presentation-20261114` / currency `JPY` / `costAmount` を照合する。**JPY15000 または期限到達**で次を実施する。

1. 専用 deploy SA を disable し、新しい CI デプロイを止める。
2. owned global forwarding rule `rangle-stg-http`、`rangle-stg-https` を削除し、owned global IP `rangle-stg-edge` を解放する。各 Compute global operation を固定 project の endpoint で最大60回・5秒間隔で確認する。API 応答から任意の URL を呼ばず、target path は固定3件、operation 名は形を検証する。
3. owned Cloud Run `rangle-stg` があれば削除し、Run LRO を最大120回・5秒間隔で poll、不在を再確認する。
4. すべて成功した場合だけ private inputs の `guard/presentation-stop.json` に非上書き receipt を残し、通知を ack、Scheduler 自身を pause する。失敗は ack せず再試行する。SA disable / Scheduler pause は Terraform で戻さない。

forwarding rule の delete 権限は公式 `resource.name` 対応表に従い、上記2名だけの条件付き project custom role。**global address は同じ対応表に含まれないため、per-address IAM が成立するとは主張しない。** `compute.globalAddresses.delete` だけの project custom role を使い、実コードの削除先を `rangle-stg-edge` に固定する。確認用の forwarding rule/address get と globalOperations.get は project read-only custom role。他の mutation を含む network admin は付けない。[IAM resource attributes](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)、[Forwarding rule delete](https://docs.cloud.google.com/compute/docs/reference/rest/v1/globalForwardingRules/delete)、[Global address delete](https://docs.cloud.google.com/compute/docs/reference/rest/v1/globalAddresses/delete)

Scheduler の pause は project scope、Workflows 起動も project scope。guard に pause 1 permission、専用 scheduler SA に `workflows.executions.create` だけを付与する。いずれも外部 WIF / CI に公開しない。Run mutation は service IAM、deployer disable は deploy SA IAM、budget subscription / receipt write は各 resource に限定する。引数だけで期限前に強制停止できる public handler は作らない。[Scheduler IAM](https://docs.cloud.google.com/scheduler/docs/access-control)、[Workflows scope](https://docs.cloud.google.com/iam/docs/roles-permissions/workflows)

JPY30000 は日本銀行2026-11-06中心相場157.69円/USDで参考約190ドル、停止額15000円は約95ドル。請求換算率とは一致しない。[日本銀行](https://www.boj.or.jp/statistics/market/forex/fxdaily/fxlist/fx261106.pdf)。課金通知の遅延、15分間隔、処理中の要求、LB/CDN/cache 反映、保存・ログの残費用があるため、総額 USD300 の厳密な hard cap とは呼ばない。停止後も private data / state / AR を保持するので保存費用は残る。後片付けは所有資源・保持対象を確認して行い、project billing や無関係な資源を停止しない。

## 検証と到達点

外部の疎通確認は `/health` を使う。Cloud Run は末尾が `z` の一部pathを予約しており、`/healthz` はコンテナへ届く前に404になる。既存の内部startup probe向けに `/healthz` も維持する。[Cloud Runの既知事項](https://docs.cloud.google.com/run/docs/known-issues#reserved-url-paths)

ローカルでは fmt、init `-backend=false`、validate、mock provider の3 plan（domain未定、domain確定＋media/budget、domain確定＋app）と rendered Workflow YAML を確認する。serverless NEG は Compute health check / backend timeout を設定せず、アプリ startup probe は `/healthz`。実 Google API の compiler、IAM 実効、TLS、Range/CORS/hash、quota・費用・停止完了は別の実環境確認で判断する。特に、期限・予算到達時の破壊的な削除分岐とその実 IAM は未検証で、無断の停止 drill は実行していない。

2026-11-08 に Cloud Run・NEG・LB・CDN・private GCS を構築した。Cloud Run URL の health/UI/API、media の HTTPS・Range・CORS・ETag・実ファイルのハッシュ一致と、private origin の匿名アクセス拒否を確認済み。その後、appのGoogle-managed certificateと独自ドメインのHTTPS、GitHub WIF経由の配備も確認した。image・catalog・configごとの配備記録は[CI/CDのrelease receipt](cicd.md#release-receipt)に従い、ActionsのSummaryとartifactへ残す。

Workflow compiler、Scheduler の OAuth 起動、期限前の `stopped=false / reason=not_due`、費用0円の検証通知の decode・閾値判定・ack を確認済み。GCS PAP/UBLA・public IAM 不在、cache-fill viewer、TTL86400秒、project 全 service の JPY30000 budget と通知先も API で確認した。検証通知の値は実請求額を表さない。

API が省略する ownership scope は、`ALL_USERS` と同義の未指定を使い永久差分を避ける。[Budget API](https://docs.cloud.google.com/billing/docs/reference/budget/rest/v1/billingAccounts.budgets#OwnershipScope)。gcloud による image 更新と候補 tag の付与を分け、実環境の Terraform plan が差分ゼロになることも確認した。
