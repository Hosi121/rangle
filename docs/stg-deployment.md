# 発表用 stg の配備

発表までの UI/API を購入ドメインなしで公開する構成。対象は `comin-up`（project number `113704194486`）、Tokyo `asia-northeast1`、期限は **2026-11-14 15:00 UTC / 11月15日00:00 JST**。承認された総予算は USD300。10万同時視聴の容量実証とは区別する。実配備記録は担当者が検証後に追記し、秘密の tfvars・plan・token は公開しない。

## 配信と所有範囲

- Go＋Flutter Web は Cloud Run `rangle-stg` の標準 `run.app` HTTPS に置く。all ingress、既定 URI 有効、公開 invoker、request billing、1vCPU/512MiB、min0/max1、concurrency40、timeout30秒。`/` は landing、`/demo/*` は Web、API は root のまま。独自 LB、GKE、GPU を作らない。
- 公開許諾済み media / manifest は新しい専用 Firebase Hosting site `rangle-stg-113704194486` の `https://rangle-stg-113704194486.web.app` から直接配信する。Go の `-asset-origin` をここへ固定し、asset body を Go で中継しない。Hosting は全配置 bytes が公開される。private GCS media bucket は耐久 mirror で、公開 origin にはしない。
- private catalog を `/mnt/catalog/snapshots/<sha256>.json` へ readonly mount する。catalog bucket は FUSE の制約から versioning 無効。runtime SA の権限はその bucket の objectViewer だけ。catalog / inputs / media mirror / state は PAP enforced、別 bucket。研究データ・weights・ローカル資産を自動公開しない。[GCS mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)、[FUSE 制約](https://docs.cloud.google.com/storage/docs/cloud-storage-fuse/overview#limitations)
- 既存 AR `rangle-stg` は既定で data として参照する。設定や image を管理変更しない。既存 runtime/deploy SA を採用する場合は対象の2つだけを import し、鍵を作成・削除しない。将来用の `environments/main` と `modules/platform` はこの配備で使わない。

## Terraform と初回手順

Terraform 1.17.1、google / google-beta 8.10.0 を固定。beta provider は Firebase project/site だけに使う。[Firebase Terraform](https://firebase.google.com/docs/projects/terraform/get-started)

1. `infra/gcp/presentation-bootstrap` の required inputs は `project_id`、`region`、`state_bucket_name`。今回は state bucket を `rangle-stg-113704194486-tfstate` とする。bootstrap はローカル state で、この1 bucket だけを作る。
2. `infra/gcp/environments/presentation` を同 bucket / prefix `rangle/presentation-stg` の GCS backend に接続する。実値は private tfvars に設定する。認証は実行元 ENV の `GOOGLE_OAUTH_ACCESS_TOKEN`。provider と GCS backend へ渡し、引数・HCL・state に token を保存しない。ADC や SA key を前提にしない。[GCS backend](https://developer.hashicorp.com/terraform/language/backend/gcs)
3. `initialize_firebase=true` は Firebase 未追加と確認した場合だけ使う。Firebase の追加は取り消せない。新規 Hosting site だけを作り、他の site は import / 変更しない。[Hosting site](https://registry.terraform.io/providers/hashicorp/google-beta/8.10.0/docs/resources/firebase_hosting_site)
4. `application=null` で foundation の差分を確認する。private bucket、site、SA/WIF、budget topic/subscription、停止 guard を先に用意し、空 catalog でアプリを起動しない。
5. publisher が許諾・allowlist・bytes/hash/header を検証した bundle だけを GCS mirror と専用 Hosting へ配置し、最後に実 catalog snapshot を private bucket へ新規保存する。preview / scene の絶対 Hosting URL は Go がローカル UI ファイルとして pin しない形にする。同名 key を上書きしない。Terraform 自体は asset を upload しない。
6. immutable `application.image_digest` と実 `application.catalog_sha256` を設定し、再度差分を確認して適用する。以降の CI は image と traffic を所有する。Terraform は image / traffic を ignore し、catalog 引数、mount、スケール、公開設定、IAM を保つ。CI が正常 revision へ rollback した後、別の IaC apply で LATEST を再昇格させない。catalog / runtime 設定の変更前には現行の正常 revision へ traffic を明示固定し、適用でできた新 revision は smoke 後に手動または CI で昇格させる。
7. UI/API/health、Hosting GET/HEAD/Range/CORS、catalog snapshot、guard の通常実行、GitHub WIF の許可・拒否条件、既存費用 cap の挙動を確認して公開 URL を記録する。

必須変数は `project_id`、`project_number`、`region`、`hosting_site_id`、`budget_currency`、`budget_stop_amount`。`deadline` は上記期限に固定。既存 AR では `create_artifact_repository=false`。`application` と下記 `budget` は nullable。`.example` は fake identifiers で、実 project/account/email は private tfvars へ置く。

## CI の接続契約

`deployment` output は app URI、image base、runtime/deploy SA、WIF provider、Hosting origin、bucket、catalog object、deadline を返す。

| 項目 | 今回の値 |
| --- | --- |
| image base | `asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server` |
| deploy SA | `rangle-stg-deploy@comin-up.iam.gserviceaccount.com` |
| WIF provider | `projects/113704194486/locations/global/workloadIdentityPools/rangle-stg-github/providers/deploy-stg` |
| GitHub repository / owner ID | `1448803636` / `128687243` |
| 許可 ref | `refs/heads/stg` |
| 許可 workflow | `Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg` |
| 許可 event | `push`、`workflow_dispatch` |

標準 OIDC claim の `workflow_ref` を使い、reusable workflow 専用の `job_workflow_ref` を要求しない。WorkloadIdentityUser、runtime actAs、Run developer に期限条件を付ける。AR repository IAM は今回の実適用で条件付き policy を拒否したため、AR writer は専用 repo のみに条件なしで付け、WIF の期限条件と guard による deploy SA disable で制限する。CI は既存の当該 Run service の更新と当該 AR repo push に限り、project-wide Run developer、Terraform apply、Hosting 管理、IAM 変更は許可しない。operation の poll 用に `run.operations.get` の project read-only custom role だけを追加する。CI 自身も deadline を確認してから認証・push・deploy する。[WIF pipeline](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)

## 予算と停止 guard

請求通貨は JPY。専用 budget は全 service・単一 project `projects/113704194486`、`ownership_scope=ALL_USERS`、custom period `2026-11-08`〜`2026-11-15`、credits 除外、30,000円、threshold 25/50/75%、project owner 通知を使い、既定 billing-account recipient は無効にする。`budget={billing_account_id,amount,start_date,end_date}` を設定すれば google_billing_budget で管理でき、billing account data source は参照しない。手動作成した場合は当該 budget だけを import する。

既存の **Cloud Run 200円 spend cap** は変更・解除しない。アプリはその cap に先に止められる可能性がある。Cloud Run spend cap（service `152E-C115-5142`）は Firebase Hosting / GCS / AR を止めず、estimated cost の反映にも遅れがある。[公式 spend cap](https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps)

Budget 通知の publisher は Google 公式の `billing-budget-alert@system.gserviceaccount.com` に topic 単位の Pub/Sub Publisher を付与する。project 固有の service agent の新規作成は不要。[公式 principal 一覧](https://docs.cloud.google.com/organization-policy/restrict-domains)

専用 `budget_guard` output は topic / subscription / workflow / scheduler / receipt path を返す。今回の通知先は `projects/comin-up/topics/rangle-stg-budget`、pull は `projects/comin-up/subscriptions/rangle-stg-budget-guard`。budget display name は `rangle-stg-presentation-20261114`、通貨 `JPY`、早期停止額 **15,000円** にする。15分ごとに Scheduler が OAuth で Workflow を実行し、subscription 内の本人 budget の `costAmount` を検査する。通貨不一致は失敗として扱い、勝手な換算はしない。期限前に入力引数だけで強制停止させる public handler は持たない。[Budget Pub/Sub](https://docs.cloud.google.com/billing/docs/how-to/budgets-programmatic-notifications)、[Scheduler OAuth](https://docs.cloud.google.com/scheduler/docs/http-target-auth)

15,000円以上または期限到達で、専用 deploy SA を disable、専用 Hosting site へ `SITE_DISABLE` release、当該 `rangle-stg` service だけを DELETE する。DELETE の operation 名は固定 project/region の prefix を検証し、最大120回・5秒間隔で完了を poll する。その後、project read-only `run.services.get` で固定 service の不在を確認するため、service IAM が削除されても再実行できる。未完了や権限エラーでは budget を ack せず retry に残す。成功後は private inputs bucket の `guard/presentation-stop.json` を条件付き新規作成し、通知を ack して自分の Scheduler を pause する。SA disable と job pause は Terraform の refresh/apply で戻さない。[Hosting SITE_DISABLE](https://firebase.google.com/docs/reference/hosting/rest/v1beta1/sites.releases/create)、[Run DELETE](https://docs.cloud.google.com/run/docs/reference/rest/v2/projects.locations.services/delete)

IAM の制約は明示する。Hosting は custom role / site IAM に対応しないため、専用 guard SA だけに project の Hosting Admin が必要。Scheduler も job 単位 IAM/Conditions に対応しないため、guard の `cloudscheduler.jobs.pause` は project custom role。Workflow 起動も project scope なので、専用 scheduler SA に `workflows.executions.create` だけを付ける。これらのアカウントを CI や外部 WIF に公開せず、コードが変更する URL は所有する site/service/job に固定する。guard の Run mutation は service IAM、deployer disable は deploy SA IAM、subscription read と receipt write は各 resource に限定する。[Firebase IAM 制約](https://firebase.google.com/docs/projects/iam/permissions#hosting)、[Scheduler IAM 制約](https://docs.cloud.google.com/scheduler/docs/access-control)、[Workflows scope](https://docs.cloud.google.com/iam/docs/roles-permissions/workflows)

30,000円は、日本銀行2026-11-06中心相場157.69円/USDで参考約190ドル、15,000円は約95ドル。請求換算率や為替変動とは一致しない。[日本銀行](https://www.boj.or.jp/statistics/market/forex/fxdaily/fxlist/fx261106.pdf)。通知の遅延、15分間隔、進行中の要求、Hosting cache 反映、保存・ログ等の残費用があるため、これを総額 USD300 の厳密な hard cap と呼ばない。残りの余裕を保持し、実請求を確認する。停止後も private data / state / AR を保持するため、保存費用は残る。発表後の削除は所有資源の一覧と保持すべきデータを確認して行い、project billing 全体や他サービスを無効にしない。

## この変更でのローカル検証

`presentation-bootstrap`、`environments/presentation`、`modules/presentation` の fmt、`init -backend=false`、validate と、mock provider の foundation / bounded app / optional budget の3 plan を使う。rendered Workflow の YAML もローカルで parse する。これらは実 API の IAM・Workflow compiler・token・課金・実公開を検証しない。実稼働の確認と配備到達点は以下へ追記する。

配備記録: この文書作成時点ではコード検証まで。実 apply と URL・CI・停止 guard の実機確認は配備担当者が記録する。
