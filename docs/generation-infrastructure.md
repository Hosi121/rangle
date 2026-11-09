# GS生成基盤

公開Cloud Runから、IAM認証必須の生成manager、専用Firestore実行台帳、Vertex AIの学習、GCS/CDNの出力配信へ接続する。GCPの生成経路と、独立した私的な検証経路は接続しない。

## 接続契約

`application.generation_config_sha256` は nullable、既定 `null`。設定は既存private catalog bucketの `generation/<sha256>.json` に非上書き配置し、managerのreadonly mountから読む。配備時にcontent SHAを確認し、Go loaderは内容を検証する。image / traffic / client metadataは既存どおりCI、args / mount / IAMはTerraformが所有する。

同じ `rangle-server` imageを次の2つのmodeで動かす。

- 公開UI/community: `-generation-manager-url https://<manager-base.run.app>`。既存catalog・community・liveのflagsは保持する。`-generation-config`、`-generation-sync-audience`、manager identity flagsとの併用は起動時に拒否する。
- private manager: `-generation-manager -generation-config /mnt/catalog/generation/<sha256>.json -catalog /mnt/catalog/snapshots/<sha256>.json -generation-manager-audience https://<manager-base.run.app> -generation-caller-service-account <public-runtime-SA> -generation-sync-audience https://<manager-base.run.app>`。community flags、`RANGLE_FIRESTORE_PROJECT`、UI・live・asset配信flagsは渡さない。catalogは既存clip IDとの掲載衝突を防ぐためにだけ読む。

managerのservice・SA・databaseは `rangle-stg-generation`。JSONはproject / 必須の `project_number` / `database: "rangle-stg-generation"` / pinned trainer image / trainer SA / 既知input URIとSHA / media origin / deadline / attempts / reviewed presetsを保持する。実値やdatasetをTerraformへ埋め込まない。managerだけに生成DBのCRUD、期限付きVertex権限、trainer actAsを渡す。公開runtimeにはcommunity DBのCRUD、catalog read、managerの `roles/run.invoker` だけを渡し、生成DB・Vertex・trainer actAsを渡さない。trainerにはFirestore権限を渡さない。

公開runtimeはGoogle ID tokenをmanager base URL audienceで取得し、platform用の `Authorization` と、署名を独立検証する `X-Rangle-Service-Authorization` の両方へ渡す。managerは署名・issuer・audience・期限・数値subject・verified emailと指定したpublic-runtime SAの完全一致を検証する。Cloud Runがplatform headerの署名を除去する場合も、application headerの検証を省略しない。[Cloud Run service authentication](https://docs.cloud.google.com/run/docs/authenticating/service-to-service)

既存のadmin cookie・現在のaccount role・CSRFを公開runtimeで確認した後、owner IDだけをmanagerへ渡す。managerは信頼したruntimeからのowner転送を受け、既存の所有者確認、active 1件、最大10 attempts、固定preset/image、30分timeout、deadlineを自ら実行する。公開runtimeが侵害された場合、残る枠内の正規preset操作とowner転送は可能だが、台帳のreset、budgetの変更、任意のVertex spec送信はできない。managerはdocument CRUDもraw Vertex APIも公開しない。公開フィード・生成detail・engagementの存在確認も、runtime identityでmanagerへ問い合わせ、入力URIやjob内部情報を返さない。

既存環境の切替前に、旧community DBの `generation_jobs/*` と `generation_control/*` を生成DBへFirestoreの型・ID・全fieldを保ったまま移す。`generation_control/admission` のused/active、`generation_control/public_feed`、全jobのowner・snapshot・state・掲載状態を保持し、attempt/historyを初期化しない。旧runtimeを止めて書き込みを凍結し、移行結果を照合してからmanagerとremote公開revisionを有効化する。新しいDBを空のまま有効化して枠を再発行しない。切替後は旧DBへの生成書き込み経路を戻さない。

ローカル開発では既存の単一process modeも残す。クラウドの公開runtimeはremote modeに固定する。disable時は既存jobがterminalであることを確認してからconfig / IAMを外す。

## ライブ生成

optionalな `live` configと `application.generation_live_enabled` で、専用worker image・identityを追加する。生成APIと実行台帳・GPU型・時間・累計attempt枠・停止guardは共用する。ライブidentityの上書き権限はmetadata pointerだけに限定し、offline trainerの権限は広げない。手順とデータ経路は[クラウドLIVE](cloud-live.md)。

## 状態同期

`generation_sync_enabled` は既定false。有効時は専用SA `rangle-stg-generation-sync@<project>.iam.gserviceaccount.com` のOIDCでmanagerの `/internal/generation/sync` を2分ごとに呼ぶ。audienceはmanager base URLを指定する。Schedulerの通常の署名付き `Authorization` をmanagerでも検証し、署名を除去する `X-Serverless-Authorization` は設定しない。署名がないtokenは拒否する。従来の単一process mode向けexact endpoint audienceも受け付けるが、issuer・email・expiry検証は共通で保持する。同期は台帳のactive 1件だけを更新し、submit・attemptの追加や返金は行わない。初回有効化時やimage/audienceの対応を変える際は、新revisionの配備までSchedulerをpauseし、配備後にresumeしてOIDC疎通を確認する。署名・期限と停止の詳細は[掲載・状態同期](generated-publication.md)を参照。

## GPU と費用の境界

Go の CustomJob request と immutable config が **Tokyo / SPOT T4×1 / offline n1-standard-4・live n1-standard-16 / replica1 / pd-standard100GB / timeout1800s / disableRetries=true** を固定する。公開 inference endpoint、GCE / Batch VM、常駐 GPU は作らない。worker は job 作成時刻+30分の絶対期限を引数でも受け、queue / restart 後に期限を過ぎていれば即終了する。[Custom training compute](https://docs.cloud.google.com/vertex-ai/docs/training/configure-compute)

1件の global active job を Firestore transaction で守り、最大10 attempts、1 attempt 当たり USD2 を予約して GPU 総予算 USD20 に収める方針。部分成功・応答不明の submit は予約を解放して盲目的に再送しない。Terraform IAM 自体は GPU 型、並列数、job 価格を強制する仕組みではない。Tokyo SKU、起動費用、Spot availability / preemption、実請求は実行条件と照合する。既存の全体 USD300、JPY30000 budget / JPY15000 guard、JPY200 Cloud Run cap は保持し、Cloud Run cap が Vertex 利用を止めるとは扱わない。

## 実行権限

- 専用trainer SAは `rangle-stg-trainer@comin-up.iam.gserviceaccount.com`。private managerだけがこのSAに期限付きactAsを持つ。
- private managerの追加権限は `aiplatform.customJobs.{create,get,list,cancel}` の4件で既存期限まで。公開runtimeはこの権限を持たない。guardは取消後の確認に必要な `list/cancel` の2件を期限後も持つ。CustomJob / Locationの `resource.name` 条件は公式対応表で確認できないため、これらは **project custom role** と明示し、region/job/label単位IAMが成立するとは主張しない。APIは固定Tokyo endpointとreviewed configを使用する。[IAM resource attributes](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)
- trainer の入力は inputs bucket の `datasets/` prefix に条件を付けた `storage.objects.get` だけ。`storage.objects.list` は bucket に対する権限で object prefix へ制限できないため付けず、worker は既知 URI を直接取得する。出力は media bucket の `generated/` だけに objectCreator、非上書き `if_generation_match=0`。delete / 上書き / catalog / account data への権限は付けない。いずれも既存期限で切る。[Storage conditions](https://docs.cloud.google.com/storage/docs/access-control/iam#conditions)
- image pull は既定 Vertex service agent の責務で、同 project の既存 AR を使う。生成有効時だけ、この project の `service-${project_number}@gcp-sa-aiplatform.iam.gserviceaccount.com` に公式 `roles/aiplatform.serviceAgent` を付け、app はその binding を待つ。runtime / deployer / trainer にはこの role を付けない。trainer の AR role、追加の service-agent reader / token creator も既定では付けない。[Custom service account](https://docs.cloud.google.com/vertex-ai/docs/general/custom-service-account)
- stdout/stderr の structured JSON は platform が収集するため、直接 Logging SDK を使わない trainer へ logsWriter は追加しない。[Container logs](https://docs.cloud.google.com/vertex-ai/docs/training/code-requirements#write_code_to_return_container_logs)

`generated/` は公開 media CDN の配信領域なので、入力や利用者の private data を配置しない。workerはファイル一式を保存した後に `scene.json` を公開し、APIは所有者の掲載要求で固定path・サイズ・SHA256・出典を検証する。フィード掲載と共有リンクの公開性は別に扱う。

初回は API を有効にした後、Service Usage の `POST /v1beta1/projects/<project_number>/services/aiplatform.googleapis.com:generateServiceIdentity`（空の JSON body）で Google-managed identity を作り、operation 完了と返却 principal を確認してから生成を有効化する。API 有効化だけでは初回非同期 job まで identity が存在しない場合がある。事前の明示作成では通常の service-agent role が自動付与されないため、上記 binding を Terraform で管理する。固定 Google provider 8.10.0 の実 schema に identity resource はなく、公式 resource は beta provider 専用なので provider は追加せず、この初回手順を分離する。[Service agent creation](https://docs.cloud.google.com/iam/docs/create-service-agents)、[Vertex service agents](https://docs.cloud.google.com/vertex-ai/docs/general/access-control#service_agents)、[Generate identity API](https://docs.cloud.google.com/service-usage/docs/reference/rest/v1beta1/services/generateServiceIdentity)

## 期限・budget guard

既存の deploy SA / LB / Run の停止を先に試み、Run 不在確認の後に Vertex を処理する。Tokyo の `customJobs.list` を `application=rangle`、`environment=presentation-stg`、`rangle_job` 存在で filter し、pagination をたどる。取消前にも3 labels、32桁小文字 hex の `rangle_job`、同 project ID/number / Tokyo の resource name を照合する。他環境・他 project・別 region・terminal job は取り消さない。[List API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/list)

取消は best-effort 非同期で、API 成功だけを停止完了と扱わない。CANCELLING も監視対象に残し、4回連続の empty scan を確認してから success receipt / ack / Scheduler pause へ進む。HTTP timeout10秒、1 scan最大20 pages、全体約300秒の cutoff を設ける。Run の初回確認失敗では削除へ進まず、不正な deletion operation 名を含む Run 停止エラーも捕捉して GPU 取消を必ず試す。API error / 未完了は捕捉し、既存資源の停止試行を飛ばさず、private `guard/failures/<execution-id>.json` に失敗を記録して fail-closed（通知ack・guard自身のScheduler pauseなし）で再試行に残す。[Cancel API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/cancel)

終了の確認には listing visibility / cancellation latency の実検証が必要で、GPU 停止時刻や費用の厳密な hard cap は未実証。mock は IAM 宣言、既定 off、config SHA、実 render YAML の順序・filter・有限性を確認した。ローカルの Workflow subset stub では ownership 除外、pagination、4回の empty scan、300秒 / 20 pages の打切り、API 失敗の伝播を確認した。CI の `python3 infra/gcp/modules/presentation/tests/shutdown_guard_test.py` は固定 Terraform で render した YAML の Run 停止以降だけを実行し、GET 403/503・不正 operation 名・応答欠落でも GPU 取消と失敗 receipt まで進むこと、未知状態では DELETE しないこと、失敗時は ack / guard 自身の pause をしないことを確認する。これらは実 compiler / IAM / quota / worker 成果品質を証明しない。

所有判定は環境・job ID・resource name・terminal state の短い各式へ分割する。label がない job は YAML で定義した独立の空 map へ戻し、直前 job の label を再利用しない。mock は render 済み YAML に含まれる全式を調べ、区切り込み400文字以内かも確認する。[Workflows expression limit](https://docs.cloud.google.com/workflows/quotas#resource_limits)

2026-11-09にTerraformと同一sourceの実compiler検証を通過した。検証用定義は削除済みで、期限・予算停止workflowは実行していない。

Workflow変更時は配備工程で実compilerを確認する。公開create/patch APIに未定義の `validateOnly` を付けて検証した扱いにはせず、対象構成の差分を確認して反映する。[Create API](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/create)、[Patch API](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/patch)
