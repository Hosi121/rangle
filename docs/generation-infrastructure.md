# GS生成基盤

Cloud Runの生成API、Firestoreの実行台帳、Vertex AIの学習、GCS/CDNの出力配信をIaCと不変configで接続する。GCPの生成経路と、独立したB300ローカル検証経路は接続しない。

## 接続契約

`application.generation_config_sha256` は nullable、既定 `null`。app と SHA が揃ったときだけ `-generation-config /mnt/catalog/generation/<sha256>.json` を追加する。設定は既存 private catalog bucket の `generation/<sha256>.json` に非上書き配置し、既存 readonly mount から読む。配備時に content SHA を確認し、Go loader は内容を検証する。image / traffic / client metadata は既存どおり CI、args / mount / IAM は Terraform が所有する。

JSON は project / 必須の `project_number` / named Firestore database / pinned trainer image / trainer SA / 既知 input URI と SHA / media origin / deadline / attempts / reviewed presets を保持する。実値や dataset を Terraform へ埋め込まない。生成 API は既存の private `rangle-stg` Firestore と runtime IAM を使い、trainer には Firestore 権限を渡さない。先に対応 Go image と config を用意してから SHA を設定し、disable 時は既存 job が terminal であることを確認してから config / IAM を外す。

## 状態同期

`generation_sync_enabled` は既定false。有効時は専用SA `rangle-stg-generation-sync@<project>.iam.gserviceaccount.com` のOIDCで `/internal/generation/sync` を2分ごとに呼ぶ。同期は台帳のactive 1件だけを更新し、submit・attemptの追加や返金は行わない。初回有効化時やimage/audienceの対応を変える際は、新revisionの配備までSchedulerをpauseし、配備後にresumeしてOIDC疎通を確認する。署名・audience・期限と停止の詳細は[掲載・状態同期](generated-publication.md)を参照。

## GPU と費用の境界

Go の CustomJob request と immutable config が **Tokyo / SPOT T4×1 / n1-standard-4 / replica1 / pd-standard100GB / timeout1800s / disableRetries=true** を固定する。公開 inference endpoint、GCE / Batch VM、常駐 GPU は作らない。worker は job 作成時刻+30分の絶対期限を引数でも受け、queue / restart 後に期限を過ぎていれば即終了する。[Custom training compute](https://docs.cloud.google.com/vertex-ai/docs/training/configure-compute)

1件の global active job を Firestore transaction で守り、最大10 attempts、1 attempt 当たり USD2 を予約して GPU 総予算 USD20 に収める方針。部分成功・応答不明の submit は予約を解放して盲目的に再送しない。Terraform IAM 自体は GPU 型、並列数、job 価格を強制する仕組みではない。Tokyo SKU、起動費用、Spot availability / preemption、実請求は実行条件と照合する。既存の全体 USD300、JPY30000 budget / JPY15000 guard、JPY200 Cloud Run cap は保持し、Cloud Run cap が Vertex 利用を止めるとは扱わない。

## 実行権限

- 専用 trainer SA は `rangle-stg-trainer@comin-up.iam.gserviceaccount.com`。runtime はこの SA だけに期限付き actAs を持つ。
- runtime の追加権限は `aiplatform.customJobs.{create,get,list,cancel}` の4件で既存期限まで。guard は取消後の確認に必要な `list/cancel` の2件を期限後も持つ。CustomJob / Location の `resource.name` 条件は公式対応表で確認できないため、これらは **project custom role** と明示し、region/job/label 単位 IAM が成立するとは主張しない。API は固定 Tokyo endpoint と reviewed config を使用する。[IAM resource attributes](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)
- trainer の入力は inputs bucket の `datasets/` prefix に条件を付けた `storage.objects.get` だけ。`storage.objects.list` は bucket に対する権限で object prefix へ制限できないため付けず、worker は既知 URI を直接取得する。出力は media bucket の `generated/` だけに objectCreator、非上書き `if_generation_match=0`。delete / 上書き / catalog / account data への権限は付けない。いずれも既存期限で切る。[Storage conditions](https://docs.cloud.google.com/storage/docs/access-control/iam#conditions)
- image pull は既定 Vertex service agent の責務で、同 project の既存 AR を使う。生成有効時だけ、この project の `service-${project_number}@gcp-sa-aiplatform.iam.gserviceaccount.com` に公式 `roles/aiplatform.serviceAgent` を付け、app はその binding を待つ。runtime / deployer / trainer にはこの role を付けない。trainer の AR role、追加の service-agent reader / token creator も既定では付けない。[Custom service account](https://docs.cloud.google.com/vertex-ai/docs/general/custom-service-account)
- stdout/stderr の structured JSON は platform が収集するため、直接 Logging SDK を使わない trainer へ logsWriter は追加しない。[Container logs](https://docs.cloud.google.com/vertex-ai/docs/training/code-requirements#write_code_to_return_container_logs)

`generated/` は公開 media CDN の配信領域なので、入力や利用者の private data を配置しない。workerはファイル一式を保存した後に `scene.json` を公開し、APIは所有者の掲載要求で固定path・サイズ・SHA256・出典を検証する。フィード掲載と共有リンクの公開性は別に扱う。

初回は API を有効にした後、Service Usage の `POST /v1beta1/projects/<project_number>/services/aiplatform.googleapis.com:generateServiceIdentity`（空の JSON body）で Google-managed identity を作り、operation 完了と返却 principal を確認してから生成を有効化する。API 有効化だけでは初回非同期 job まで identity が存在しない場合がある。事前の明示作成では通常の service-agent role が自動付与されないため、上記 binding を Terraform で管理する。固定 Google provider 8.10.0 の実 schema に identity resource はなく、公式 resource は beta provider 専用なので provider は追加せず、この初回手順を分離する。[Service agent creation](https://docs.cloud.google.com/iam/docs/create-service-agents)、[Vertex service agents](https://docs.cloud.google.com/vertex-ai/docs/general/access-control#service_agents)、[Generate identity API](https://docs.cloud.google.com/service-usage/docs/reference/rest/v1beta1/services/generateServiceIdentity)

## 期限・budget guard

既存の deploy SA / LB / Run の停止を先に試み、Run 不在確認の後に Vertex を処理する。Tokyo の `customJobs.list` を `application=rangle`、`environment=presentation-stg`、`rangle_job` 存在で filter し、pagination をたどる。取消前にも3 labels、32桁小文字 hex の `rangle_job`、同 project ID/number / Tokyo の resource name を照合する。他環境・他 project・別 region・terminal job は取り消さない。[List API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/list)

取消は best-effort 非同期で、API 成功だけを停止完了と扱わない。CANCELLING も監視対象に残し、4回連続の empty scan を確認してから success receipt / ack / Scheduler pause へ進む。HTTP timeout10秒、1 scan最大20 pages、全体約300秒の cutoff を設ける。API error / 未完了は捕捉し、既存資源の停止試行を飛ばさず、private `guard/failures/<execution-id>.json` に失敗を記録して fail-closed（通知ack・guard自身のScheduler pauseなし）で再試行に残す。[Cancel API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/cancel)

終了の確認には listing visibility / cancellation latency の実検証が必要で、GPU 停止時刻や費用の厳密な hard cap は未実証。mock は IAM 宣言、既定 off、config SHA、実 render YAML の順序・filter・有限性を確認した。ローカルの Workflow subset stub では ownership 除外、pagination、4回の empty scan、300秒 / 20 pages の打切り、API 失敗の伝播を確認した。これらは実 compiler / IAM / quota / worker 成果品質を証明しない。

所有判定は環境・job ID・resource name・terminal state の短い各式へ分割する。label がない job は YAML で定義した独立の空 map へ戻し、直前 job の label を再利用しない。mock は render 済み YAML に含まれる全式を調べ、区切り込み400文字以内かも確認する。[Workflows expression limit](https://docs.cloud.google.com/workflows/quotas#resource_limits)

2026-11-09にTerraformと同一sourceの実compiler検証を通過した。検証用定義は削除済みで、期限・予算停止workflowは実行していない。

Workflow変更時は配備工程で実compilerを確認する。公開create/patch APIに未定義の `validateOnly` を付けて検証した扱いにはせず、対象構成の差分を確認して反映する。[Create API](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/create)、[Patch API](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/patch)
