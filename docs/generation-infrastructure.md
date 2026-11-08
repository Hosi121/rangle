# GS 生成基盤の IaC 準備

この変更は予定構成の HCL / mock 検証まで。実 apply、API 有効化、CustomJob 起動、dataset / config upload は行っていない。GCP の生成経路と、独立した B300 ローカル検証経路は接続しない。

## 接続契約

`application.generation_config_sha256` は nullable、既定 `null`。app と SHA が揃ったときだけ `-generation-config /mnt/catalog/generation/<sha256>.json` を追加する。設定は既存 private catalog bucket の `generation/<sha256>.json` に非上書き配置し、既存 readonly mount から読む。中身と content SHA は配備担当者と Go loader が検証する。image / traffic / client metadata は既存どおり CI、args / mount / IAM は Terraform が所有する。

JSON は project / 必須の `project_number` / named Firestore database / pinned trainer image / trainer SA / 既知 input URI と SHA / media origin / deadline / attempts / reviewed presets を保持する。実値や dataset を Terraform へ埋め込まない。生成 API は既存の private `rangle-stg` Firestore と runtime IAM を使い、trainer には Firestore 権限を渡さない。先に対応 Go image と config を用意してから SHA を設定し、disable 時は既存 job が terminal であることを確認してから config / IAM を外す。

## GPU と費用の境界

Go の CustomJob request と immutable config が **Tokyo / SPOT T4×1 / n1-standard-4 / replica1 / pd-standard100GB / timeout1800s / disableRetries=true** を固定する。公開 inference endpoint、GCE / Batch VM、常駐 GPU は作らない。worker は job 作成時刻+30分の絶対期限を引数でも受け、queue / restart 後に期限を過ぎていれば即終了する。[Custom training compute](https://docs.cloud.google.com/vertex-ai/docs/training/configure-compute)

1件の global active job を Firestore transaction で守り、最大10 attempts、1 attempt 当たり USD2 を予約して GPU 総予算 USD20 に収める方針。部分成功・応答不明の submit は予約を解放して盲目的に再送しない。Terraform IAM 自体は GPU 型、並列数、job 価格を強制する仕組みではない。最終 Tokyo SKU、起動費用、Spot availability / preemption、実請求は配備前に確認する。既存の全体 USD300、JPY30000 budget / JPY15000 guard、JPY200 Cloud Run cap は保持し、Cloud Run cap が Vertex 利用を止めるとは扱わない。

## 実行権限

- 専用 trainer SA は `rangle-stg-trainer@comin-up.iam.gserviceaccount.com`。runtime はこの SA だけに期限付き actAs を持つ。
- runtime の追加権限は `aiplatform.customJobs.{create,get,list,cancel}` の4件で既存期限まで。guard は取消後の確認に必要な `list/cancel` の2件を期限後も持つ。CustomJob / Location の `resource.name` 条件は公式対応表で確認できないため、これらは **project custom role** と明示し、region/job/label 単位 IAM が成立するとは主張しない。API は固定 Tokyo endpoint と reviewed config を使用する。[IAM resource attributes](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)
- trainer の入力は inputs bucket の `datasets/` prefix に条件を付けた `storage.objects.get` だけ。`storage.objects.list` は bucket に対する権限で object prefix へ制限できないため付けず、worker は既知 URI を直接取得する。出力は media bucket の `generated/` だけに objectCreator、非上書き `if_generation_match=0`。delete / 上書き / catalog / account data への権限は付けない。いずれも既存期限で切る。[Storage conditions](https://docs.cloud.google.com/storage/docs/access-control/iam#conditions)
- image pull は既定 Vertex service agent の責務で、同 project の既存 AR を使う。trainer へ AR role、追加の service-agent reader / token creator を既定で付けない。必要な既存 service-agent role は親が実環境で確認する。[Custom service account](https://docs.cloud.google.com/vertex-ai/docs/general/custom-service-account)
- stdout/stderr の structured JSON は platform が収集するため、直接 Logging SDK を使わない trainer へ logsWriter は追加しない。[Container logs](https://docs.cloud.google.com/vertex-ai/docs/training/code-requirements#write_code_to_return_container_logs)

`generated/` は公開 media CDN の配信領域なので、入力や利用者の private data を配置しない。ファイル一式を検証し、manifest を最後に広告する工程は worker / API の責務である。

## 期限・budget guard

既存の deploy SA / LB / Run の停止を先に試み、Run 不在確認の後に Vertex を処理する。Tokyo の `customJobs.list` を `application=rangle`、`environment=presentation-stg`、`rangle_job` 存在で filter し、pagination をたどる。取消前にも3 labels、32桁小文字 hex の `rangle_job`、同 project ID/number / Tokyo の resource name を照合する。他環境・他 project・別 region・terminal job は取り消さない。[List API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/list)

取消は best-effort 非同期で、API 成功だけを停止完了と扱わない。CANCELLING も監視対象に残し、4回連続の empty scan を確認してから success receipt / ack / Scheduler pause へ進む。HTTP timeout10秒、1 scan最大20 pages、全体約300秒の cutoff を設ける。API error / 未完了は捕捉し、既存資源の停止試行を飛ばさず、private `guard/failures/<execution-id>.json` に失敗を記録して fail-closed（通知 ack / pause なし）で再試行に残す。[Cancel API](https://docs.cloud.google.com/gemini-enterprise-agent-platform/reference/rest/v1/projects.locations.customJobs/cancel)

終了の確認には listing visibility / cancellation latency の実検証が必要で、GPU 停止時刻や費用の厳密な hard cap は未実証。mock は IAM 宣言、既定 off、config SHA、実 render YAML の順序・filter・有限性を確認した。ローカルの Workflow subset stub では ownership 除外、pagination、4回の empty scan、300秒 / 20 pages の打切り、API 失敗の伝播を確認した。これらは実 compiler / IAM / quota / worker 成果品質を証明しない。

公開 Workflows v1 / v1beta API の create / patch には `validateOnly` や compiler 検証専用 method がなく、非デプロイの server-side compile は確認できない。公式 REST 仕様、公開 Discovery JSON、Cloud SDK の request 定義でも確認した。SDK 内の `ValidateWorkflow` は初回 source の有無を調べる処理で、compiler ではない。実 compiler 検証は後続の配備工程に残し、本番 guard へ未定義の `validateOnly` を付けて PATCH する検証は行わない。[Create API](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/create)、[Patch API](https://docs.cloud.google.com/workflows/docs/reference/rest/v1/projects.locations.workflows/patch)、[公開 API 定義](https://github.com/googleapis/googleapis/blob/master/google/cloud/workflows/v1/workflows.proto)
