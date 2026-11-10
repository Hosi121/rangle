# GS生成基盤

公開Cloud Run → IAM必須の生成manager → 専用Firestore台帳・Vertex AI → GCS/CDNで構成する。GCP経路と私的な検証環境は接続しない。実配備・移行の証拠は[セキュリティ調査](security-review.md)。

## 接続契約

同じ `rangle-server` imageを2 modeで動かす。

| mode | flagsと禁止事項 |
| --- | --- |
| 公開UI/community | `-generation-manager-url https://<manager-base.run.app>`。catalog/community/live flagsは維持。`-generation-config`・`-generation-sync-audience`・manager identity flagsとの併用は起動拒否 |
| private manager | `-generation-manager -generation-config /mnt/catalog/generation/<sha256>.json -catalog /mnt/catalog/snapshots/<sha256>.json -generation-manager-audience https://<manager-base.run.app> -generation-caller-service-account <public-runtime-SA> -generation-sync-audience https://<manager-base.run.app>`。community flags・`RANGLE_FIRESTORE_PROJECT`・UI/live/asset配信flagsは禁止。catalogは掲載ID衝突の検出用 |

`application.generation_config_sha256` は既定 `null`。configはprivate catalog bucketの `generation/<sha256>.json` へ非上書き配置し、managerがreadonly mountで読む。配備時にcontent SHA、Go loaderで内容を検証する。image/traffic/client metadataはCI、args/mount/IAMはTerraformが所有する。

configはproject・必須 `project_number`・`database: "rangle-stg-generation"`・pinned trainer image/SA・既知input URI/SHA・media origin・deadline・attempts・reviewed presetsを持つ。実値/datasetはTerraformに埋め込まない。

| identity | 許可 |
| --- | --- |
| 公開runtime | community DB CRUD、catalog read、managerの `roles/run.invoker`。生成DB/Vertex/trainer actAsなし |
| manager（service/SA/DBは `rangle-stg-generation`） | 生成DB CRUD、期限付きVertex操作・trainer actAs。community DB権限なし |
| trainer | 下記の限定input/output。Firestore権限なし |

公開runtimeはmanager base URL audienceのGoogle ID tokenを `Authorization` と `X-Rangle-Service-Authorization` の両方に送る。managerは後者で署名・issuer・audience・期限・数値subject・verified email・指定runtime SAの完全一致を独立検証する。platformが前者の署名を除去しても検証を省略しない。[service authentication](https://docs.cloud.google.com/run/docs/authenticating/service-to-service)

公開側で各経路の運営/担当者session・現在の団体/役割・CSRFを確認し、認証済みの操作主体を転送する。[団体用の短寿命assertionと旧運営headerの契約](publisher-auth.md#団体の生成公開)に従い、ブラウザの権限headerを転送しない。managerは所有者、入力の団体許可、active 1件、最大10 attempts、固定preset/image、30分、deadlineを強制する。公開runtime侵害時にも残枠内の正規preset操作・owner偽装は可能だが、台帳reset・budget変更・任意Vertex specは許可しない。document CRUD/raw Vertex APIは公開せず、feed/detail/engagement確認も内部情報やinput URIを返さない。

旧community DBからの移行は[専用手順](security-generation-migration.md)でwritersを凍結し、native fields・ID・used/active・履歴・掲載状態を保持する。空DBでattemptを再発行せず、切替後は旧生成writerへ戻さない。クラウド公開runtimeはremote固定、ローカルは単一processも維持する。無効化はjobが全件terminalになってから行う。

## ライブ生成

optional `live` configと `application.generation_live_enabled` で専用image/identityを追加する。API・台帳・GPU型・累計attempt・停止guardを共用し、上書き権限はmetadata pointerだけに限定する。offline trainerの権限は広げない。[クラウドLIVE](cloud-live.md)

## 状態同期

`generation_sync_enabled` は既定false。有効時は `rangle-stg-generation-sync@<project>.iam.gserviceaccount.com` が2分ごとにmanagerの `/internal/generation/sync` をOIDCで呼ぶ。

- audienceはmanager base URL。通常の署名付き `Authorization` をmanagerも検証し、署名を除く `X-Serverless-Authorization` は設定しない。署名なしは拒否する。
- legacy単一processのexact endpoint audienceも受け付けるが、issuer/email/expiry検証は同じ。
- active 1件の状態だけを同期し、submit・attempt追加・返金はしない。
- 初回有効化やimage/audience変更時はSchedulerをpauseし、新revision配備後にresumeして疎通を確認する。

掲載・停止条件は[掲載・状態同期](generated-publication.md)。

## GPU と費用の境界

CustomJob requestとimmutable configで **Tokyo / SPOT T4×1 / offline n1-standard-4・live n1-standard-16 / replica1 / pd-standard100GB / timeout1800s / disableRetries=true** を固定する。常駐GPU、公開inference endpoint、GCE/Batch VMは作らない。workerにもjob作成+30分の絶対期限を渡し、queue/restart後に超過していれば即終了する。[compute設定](https://docs.cloud.google.com/vertex-ai/docs/training/configure-compute)

Firestore transactionでglobal active 1件・累計10 attemptsを守り、1 attempt USD2予約、GPU総予算USD20を目指す。部分成功/応答不明のsubmitは予約を解放して盲目的に再送しない。IAMはGPU型・並列数・価格を強制しないため、Tokyo SKU・起動費・Spot可用性/preemption・実請求は別確認とする。全体USD300、JPY30000 budget / JPY15000 guard、Cloud Run JPY200 capは[配備手順](stg-deployment.md#予算期限の停止)を維持する。Run capはVertexを止めない。

## 実行権限

| 対象 | scope・制約 |
| --- | --- |
| manager | `aiplatform.customJobs.{create,get,list,cancel}` のproject custom roleと `rangle-stg-trainer@comin-up.iam.gserviceaccount.com` のactAs。既存期限まで |
| guard | 期限後も取消確認用 `list/cancel`。CustomJob/Locationの `resource.name` 条件は公式対応未確認のため、region/job/label単位IAMとは扱わない。APIはTokyo固定 |
| trainer input | inputsの `datasets/` に条件付き `storage.objects.get`。bucket-levelのlistは付けず既知URIを直接取得 |
| trainer output | mediaの `generated/` にobjectCreator、`if_generation_match=0`。delete/上書き/catalog/account data権限なし。input/outputとも既存期限まで |
| image pull | 同projectのVertex service agentだけに `roles/aiplatform.serviceAgent`。runtime/deployer/trainerへ付けず、trainer AR readerや追加token creatorも既定では不要 |

[IAM resource属性](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)、[Storage条件](https://docs.cloud.google.com/storage/docs/access-control/iam#conditions)、[Vertex custom SA](https://docs.cloud.google.com/vertex-ai/docs/general/custom-service-account)。stdout/stderrはplatform収集のためLogging SDKを使わないtrainerにlogsWriterを足さない。[container logs](https://docs.cloud.google.com/vertex-ai/docs/training/code-requirements#write_code_to_return_container_logs)

`generated/` は公開CDN領域であり、input/private dataは禁止。workerは全ファイル保存後に `scene.json` を置き、所有者の掲載要求でAPIが固定path・size・SHA・出典を検証する。feed掲載と共有URLの公開性は別に扱う。

初回はAPI有効化後、Service Usageの `POST /v1beta1/projects/<project_number>/services/aiplatform.googleapis.com:generateServiceIdentity`（空JSON）でidentityを作り、operation完了とprincipalを確認する。明示作成だけではservice-agent roleが自動付与されないためTerraformでbindingし、appはこれを待つ。Google provider 8.10.0にidentity resourceはなくbeta専用なので、この初回手順を分離する。[identity作成](https://docs.cloud.google.com/iam/docs/create-service-agents)、[Vertex service agents](https://docs.cloud.google.com/vertex-ai/docs/general/access-control#service_agents)、[API](https://docs.cloud.google.com/service-usage/docs/reference/rest/v1beta1/services/generateServiceIdentity)

## 期限・budget guard

[停止処理](stg-deployment.md#予算期限の停止)はRun停止に失敗してもTokyoのGPU取消を試す。

| 検査・上限 | 契約 |
| --- | --- |
| 対象 | `application=rangle`・`environment=presentation-stg`・32桁小文字hexの `rangle_job`、同project ID/number・Tokyoのresource nameを取消前にも照合。他環境/region/project・terminalを除外 |
| scan | HTTP10秒、最大20 pages/scan、全体約300秒。paginationをたどる |
| 完了 | 非同期cancel後もCANCELLINGを監視し、4連続empty scanで完了 |
| 失敗 | private `guard/failures/<execution-id>.json` に記録し、通知ack/guard自身pauseをせず再試行 |

[list](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/list)、[cancel](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/cancel)。所有判定式は分割し、labels欠落時は独立した空mapを使う。全式は区切り込み400文字以内。[式の制限](https://docs.cloud.google.com/workflows/quotas#resource_limits)

mock/stubと `python3 infra/gcp/modules/presentation/tests/shutdown_guard_test.py` は所有除外・pagination・cutoff・API/Run失敗でもGPU取消を継続すること、未知状態でDELETEせず失敗時にack/pauseしないことを検証する。実IAM/quota/取消遅延・停止時刻・hard cap・成果品質は証明しない。

2026-11-09に実compiler検証を通過し、一時定義は削除済み。停止分岐は未実行。変更時も実compilerで確認し、未定義 `validateOnly` を[create](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/create)/[patch](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/patch)へ付けた検証扱いにしない。
