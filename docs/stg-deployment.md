# 発表用 stg の配備

対象は `comin-up`（`113704194486`）、Tokyo `asia-northeast1`。公開appは `https://app.rangleplay.stream`、mediaは `https://media.rangleplay.stream`。承認予算USD300、停止期限 **2026-11-14T15:00:00Z（11月15日00:00 JST）**。10万同時視聴の容量は未実証。

## 配信と初回配備の入力

| 入力・経路 | 契約 |
| --- | --- |
| `base_domain=null`（既定） | private GCS/CDN backend、cache-fill IAM、global IPv4 `rangle-stg-edge` まで。app/certificate/proxy/forwarding rule/URL mapは未作成。IP予約費用は残り得る |
| domain設定後 | 外部HTTPS LBでapp/media hostを分離。appはCDNなしのNEG→Run、mediaはCDN bucket。API/UI/catalog/inputをmediaへ置かない |
| 公開Run `rangle-stg` | Go＋Flutter Web、1vCPU/512MiB、request billing、min0/max1、concurrency40、timeout30秒。`/`・`/demo/*`・APIをpath変換せず渡す。asset originは `https://media.<base_domain>` |
| 公開ingress | `internal-and-cloud-load-balancing`。既定 `run.app` / tag URLへの外部直通は拒否。URIはGoogle-managed caller互換のため保持。アプリ認証/CSRF/所有者認可は必要 |
| private生成manager | IAM必須 `rangle-stg-generation`。公開appはremote APIだけを呼び、生成DB/Vertex/actAsを持たない。[生成契約](generation-infrastructure.md) |

`deployment` outputのIP/origin/URI・DNS A recordを使い、Cloudflareでapp/mediaを同じIPへ **DNS only** で設定する。競合A/AAAAを除き、certificate ACTIVEを待つ。IaCはDNSを変更せず、Cloudflare tokenも使わない。[TLS](https://docs.cloud.google.com/load-balancing/docs/ssl-certificates/google-managed-certs)、[ingress](https://docs.cloud.google.com/run/docs/securing/ingress)

## private GCS と公開工程

既存4 bucketはPAP enforced・匿名grantなし。media `rangle-stg-113704194486-media` のobjectViewerを `service-113704194486@https-lb.iam.gserviceaccount.com` に渡し、GCS匿名直通を拒否しつつCDN公開する。[private origin/cache-fill IAM](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket#make_your_cloud_storage_bucket_private)

| media cache | 設定 |
| --- | --- |
| mode / TTL | `FORCE_CACHE_ALL`、default/client86400秒。APIが省略するmax TTLは設定しない |
| 無効 | negative cache・stale serve・dynamic compression |
| key / CORS | queryは `generation` だけ。Originは明示key指定がAPI非対応のため `Vary: Origin` を利用。匿名CORS `*`、GET/HEAD・Range用header、credentialsなし |

媒体は公開許諾済みの不変bytesだけに限定する。実CDNのGET/HEAD/206/304/416・CORS/hash/cold missを確認し、queryをcontent選択/認可に流用しない。[CDN cache](https://docs.cloud.google.com/cdn/docs/caching)

catalogのreadonly mount・versioning・新規作成限定の公開順は[Terraform共通契約](../infra/gcp/README.md#catalog-と公開工程)に従う。presentationのpreview/sceneはmedia originの絶対URLを使い、domain確定後にcatalog SHAを再計算する。Terraformはuploadしない。[mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)、[FUSE制約](https://docs.cloud.google.com/storage/docs/cloud-storage-fuse/overview#limitations)

## Terraform と CI

- Terraform **1.17.1** / Google provider **8.10.0** とlockを固定。Firebase/google-betaは使わない。旧Hosting Adminは対象memberだけ除去し、APIは `disable_on_destroy=false` で維持する。他project resourceをimport/削除しない。
- bootstrap入力は `project_id` / `region` / `state_bucket_name`。既存state bucket `rangle-stg-113704194486-tfstate`、prefix `rangle/presentation-stg` を使う。短命 `GOOGLE_OAUTH_ACCESS_TOKEN` で認証し、token/tfvars/plan/stateは非公開。SA key/ADCは前提にしない。既存ARはdata参照だけ。
- root入力は [variables.tf](../infra/gcp/environments/presentation/variables.tf) とfake exampleを参照する。初回はdomain/appをnullにし、確定domain・digest/catalog SHAでappを追加する。
- CIはimage/traffic、Terraformはargs/mount/catalog/scale/IAMを所有する。runtime変更前は正常revisionへ明示traffic固定し、applyで作るrevisionはsmoke後に昇格する。LATEST100でCI rollbackを上書きしない。同時apply/deployは避ける。[CIとreceipt](cicd.md)

### LB入口制限への切替

候補NEGは同じRunの固定tag `ci-candidate` を指し、appへの `X-Rangle-Candidate: ci-candidate` だけをCDNなし候補backendへ送る。headerは公開routing selectorで、認証を省略しない。DNS/certificate/CI network・IAM権限は追加しない。[NEG tag](https://docs.cloud.google.com/sdk/gcloud/reference/compute/network-endpoint-groups/create)、[header route](https://docs.cloud.google.com/load-balancing/docs/https/traffic-management-global)

1. 正常revision/traffic比率を記録・明示固定する。初回NEG作成前はそのrevisionへ候補tagを付ける。tagは直列deploy専用。
2. planで候補NEG/backend/host route/ingressだけを確認して適用する。media/private IAM/停止guardの不要な変更を混ぜない。
3. [CI検証・復帰](cicd.md#配備の流れ)で直通404・LB候補/公開を確認する。旧ALL ingressなら更新前に失敗。tag反映待ちは最大5回とする。
4. 生成同期は現在の専用manager endpointでOIDC疎通を確認する。公開LB向けlegacy同期へ戻さない。

2026-11-10に適用済み。直通404、LB UI/API・manager同期200を[切替記録](security-review.md)で確認した。専用VPC/subnet隔離は追加していない。

| 配備identity | 値 |
| --- | --- |
| image base | `asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/server` |
| deploy SA | `rangle-stg-deploy@comin-up.iam.gserviceaccount.com` |
| WIF provider | `projects/113704194486/locations/global/workloadIdentityPools/rangle-stg-github/providers/deploy-stg` |
| repository / owner数値ID | `1448803636` / `128687243` |
| ref / workflow | `refs/heads/stg` / `Hosi121/rangle/.github/workflows/deploy-stg.yml@refs/heads/stg` |
| events | `push` / `workflow_dispatch` |

OIDCは `workflow_ref` を使う。WorkloadIdentityUser/runtime actAs/Run developer/project read-only `run.operations.get` は期限付き。ARの条件付きpolicyが実APIで拒否されたため専用repo writerだけは無条件で、WIF期限とguardのdeploy SA disableで制限する。CIへTerraform apply/network admin/IAM/Billing操作は渡さない。[WIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)

## 予算・期限の停止

**Cloud Run JPY200 spend capを維持する**。先にRunが止まってもVertex/CDN/LB/GCS/ARは止まらない。[capの範囲](https://docs.cloud.google.com/billing/docs/how-to/budgets-spend-caps)

| budget | 設定 |
| --- | --- |
| 対象 | 単一project・全service、ownership scope未指定（`ALL_USERS`同義）、credits除外 |
| 期間・額 | `2026-11-08`〜`2026-11-15`、JPY30000、threshold25/50/75% |
| 通知 | project owner、既定billing recipientなし。`projects/comin-up/topics/rangle-stg-budget` へ公式 `billing-budget-alert@system.gserviceaccount.com` だけPublisher |
| guard | 15分ごとScheduler→OAuth Workflow。subscription `rangle-stg-budget-guard` のdisplay name `rangle-stg-presentation-20261114` / `JPY` / `costAmount` を照合し、**JPY15000または期限**で停止 |

[公式publisher](https://docs.cloud.google.com/organization-policy/restrict-domains)、[ownership scope](https://docs.cloud.google.com/billing/docs/reference/budget/rest/v1/billingAccounts.budgets#OwnershipScope)。billing account data sourceは使わない。

停止順序は次のとおり。各失敗を集約し、後続を試す。

1. deploy SAをdisable。
2. owned forwarding rule `rangle-stg-http` / `rangle-stg-https` を削除し、IP `rangle-stg-edge` を解放。固定project endpoint・検証済operation名で最大60回×5秒pollする。
3. 入口消失確認後にowned Armor attachment/policyを清掃。[Armorの所有確認とIAM](security-hardening.md#cloud-armor)
4. manager→公開Runを削除し、不在を確認。Run LROは最大120回×5秒pollし、状態不明なら削除せず失敗として残す。
5. [owned GPU job取消](generation-infrastructure.md#期限budget-guard)、生成同期pause、[両DBのowned backup schedule削除](security-hardening.md#firestore-の復旧)を試す。DB/PITR/保存済backupは残す。
6. 全成功時だけprivate inputsの `guard/presentation-stop.json` へ非上書きreceiptを保存し、通知ack・guard自身のScheduler pauseを行う。失敗は `guard/failures/<execution-id>.json` に記録し、ack/自身pauseをせず再試行する。SA disable/pauseをTerraformで戻さない。

| guard IAM | scopeの限界 |
| --- | --- |
| forwarding rule delete | 公式対応の名前条件で上記2件に制限 |
| global address delete | per-address条件非対応。単一permissionのproject custom role＋固定削除先。確認get/globalOperations.getもproject read-only |
| Scheduler pause / Workflow create | project scope。guardへpauseだけ、scheduler SAへ `workflows.executions.create` だけ。WIF/CIへ公開しない |
| Run/deployer/subscription/receipt | 各service/SA/resourceへ限定 |

network adminや期限前に引数だけで強制停止できるpublic handlerは作らない。[IAM属性](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)、[rule delete](https://docs.cloud.google.com/compute/docs/reference/rest/v1/globalForwardingRules/delete)、[IP delete](https://docs.cloud.google.com/compute/docs/reference/rest/v1/globalAddresses/delete)、[Scheduler IAM](https://docs.cloud.google.com/scheduler/docs/access-control)、[Workflow scope](https://docs.cloud.google.com/iam/docs/roles-permissions/workflows)

JPY30000/15000は参考約USD190/95（[日銀2026-11-06中心相場157.69](https://www.boj.or.jp/statistics/market/forex/fxdaily/fxlist/fx261106.pdf)）、請求換算ではない。通知遅延・15分間隔・処理中要求・cache反映・残る保存/log費用のためUSD300の厳密hard capではない。private data/state/ARを保持し、project billingや無関係な資源を止めない。

## 検証と到達点

外部healthは `/health`。`/healthz` はRun予約pathで外部404のため内部startup probeだけに使う。NEGにCompute health check/backend timeoutは設定しない。[予約path](https://docs.cloud.google.com/run/docs/known-issues#reserved-url-paths)

| 証拠 | 到達点 |
| --- | --- |
| local | fmt・backendなしinit/validate・domain/media/appのmock plan・render済Workflow |
| 2026-11-08以降 | Run/LB/CDN/private GCS、独自domain TLS、UI/API、media Range/CORS/ETag/hash、匿名origin拒否、WIF配備 |
| guard非破壊確認 | 実compiler・Scheduler OAuth、期限前 `stopped=false / reason=not_due`、0円検証通知のdecode/判定/ack（実請求ではない） |
| 2026-11-10 | [manager分離・防御](security-review.md)、[ログ監視](observability.md) |

過去のAPI確認はPAP/UBLA/IAM/cache-fill/TTL/budget通知先と差分ゼロを含む。最新Budget再読取は権限不足。破壊的停止・restore・実IAM全体・quota/100k容量・hard capは未実証。[image/catalog/config別receipt](cicd.md#release-receipt)
