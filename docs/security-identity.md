# 既定 Compute サービスアカウントの権限整理

対象は `comin-up`（`113704194486`）の既定Compute SA `113704194486-compute@developer.gserviceaccount.com`。公開runtime SAとは別identityである。以下は2026-11-10の変更根拠・再確認手順。

## 2026-11-10 の変更前調査

既定SAに無条件project `roles/editor` があり、調べた現行workloadには依存先が見つからなかった。アプリデータ・Secret値・鍵内容は読んでいない。

| メタデータ調査対象 | 結果（manager追加前） |
| --- | --- |
| Cloud Run全域・保持revision | Tokyoの `rangle-stg` のみ、jobs 0。75 revisionすべて専用runtime SA |
| Compute instances/templates/MIG/machine images | 全域0 |
| Workflows / Scheduler | guardのみ / 取得可能な34 locationでTokyoのguard・syncのみ。すべて専用SA |
| Vertex 45応答region | Tokyoの過去CustomJob 8件はtrainer/live-trainer SA。他のendpoint/pipeline/batch prediction/tuning/training pipeline/persistent resourceは0 |
| Vertex schedules | 40 regionで0、5 regionはpermission error |
| Discovery Engine global/us/eu | data stores 0 |
| Functions / Build / GKE / Batch / Workbench / Notebooks | API無効。Functions/Build listは `SERVICE_DISABLED`。有効化はしていない |
| 既定SA直接IAM / user-managed keys | bindingなし / 既定＋専用7 SAの計8 identityで0 |

Vertex `asia-southeast3` は全collectionが404、schedulesの `europe-west15` / `us-central2` / `me-central2` / `us-east7` / `us-west8` はlocation permission errorだった。これらは空と判定しない。未調査service・project外参照・削除済resource・将来build・外部impersonationの不使用証明ではない。

変更前はData Access監査無効で、ログ不在やRecommenderだけを権限整理の根拠にしていない。[Googleの既定SA整理手順](https://docs.cloud.google.com/compute/docs/instances/change-service-account)

## 適用結果（2026-11-10、private generation manager 追加前）

- 無条件Editorから既定SAの1 memberだけを削除し、代替roleを付けなかった。人のOwner/Editor・他identity・SA有効状態は維持した。
- `rangle-stg-113704194486-{catalog,inputs,media,tfstate}` から `projectViewer:comin-up` のlegacyBucketReader/legacyObjectReaderだけを除去した。projectEditor/projectOwnerと専用identityは維持した。
- 専用7 SA＋既定SA・4 bucketのcheckerは23/23合格、exit 0。**manager/専用DB追加後の分離や実データ拒否の証拠ではない**。[追加後の確認](security-review.md)

## 変更範囲と戻し方

変更直前のIAM policyを非公開保存し、member/条件を確認して1 grantだけ操作する。

```bash
gcloud projects remove-iam-policy-binding comin-up \
  --account="$GCP_ACCOUNT" --project=comin-up \
  --member=serviceAccount:113704194486-compute@developer.gserviceaccount.com \
  --role=roles/editor --condition=None
```

伝播後にcheckerと既存HTTP smokeを行う。新GPU job・Workflow・PubSub操作は不要。既定SAに起因する回帰なら、同じmember/role/`--condition=None` の `add-iam-policy-binding` で戻す。古いproject policy全体のsetは並行変更を失うため使わない。

将来依存が見つかればworkloadと公式permissionから専用SAを設計する。Storageはbucket、ARはrepositoryへ限定し、Vertex pipelineには既定Compute SAでなく専用SAを明示する。permissionごとの対応scopeを確認し、未対応のresource-level IAMを仮定しない。[Vertex pipeline identity/権限](https://docs.cloud.google.com/vertex-ai/docs/pipelines/configure-project)

## IAM evaluator の確認範囲

2026-11-10にruntime→Vertex create、runtime→生成DB get、manager→community DB getを時刻context付きPolicy Troubleshooter v3へ問い合わせた。全件HTTP403・`SERVICE_DISABLED`（`policytroubleshooter.googleapis.com`）で、**対象SAの拒否判定ではない**。Cloud Asset APIも無効、Analyzerも未実行。API有効化・IAM追加・SA impersonation・実データaccessは行っていない。[Analyzerの前提・反映遅延](https://docs.cloud.google.com/policy-intelligence/docs/analyze-iam-policies)

既にAPIが有効でpolicy/custom role閲覧権限がある場合の再確認例。`CHECK_TIME` は確認時のUTC RFC3339。global IAM評価であり、Vertex resource作成/Tokyo API呼出しはしない。

```bash
gcloud policy-intelligence troubleshoot-policy iam \
  //cloudresourcemanager.googleapis.com/projects/comin-up \
  --principal-email=rangle-stg-runtime@comin-up.iam.gserviceaccount.com \
  --permission=aiplatform.customJobs.create --request-time="$CHECK_TIME" \
  --account=takuyakataiwa@gmail.com --project=comin-up --billing-project=comin-up \
  --format='json(overallAccessState,allowPolicyExplanation.allowAccessState,denyPolicyExplanation.denyAccessState)'

check_database_iam() {
  gcloud policy-intelligence troubleshoot-policy iam \
    "//firestore.googleapis.com/projects/comin-up/databases/$2" \
    --principal-email="$1" --permission="${3:-datastore.entities.get}" \
    --resource-name="projects/comin-up/databases/$2" \
    --resource-service=firestore.googleapis.com \
    --resource-type=firestore.googleapis.com/Database --request-time="$CHECK_TIME" \
    --account=takuyakataiwa@gmail.com --project=comin-up --billing-project=comin-up \
    --format='json(overallAccessState,allowPolicyExplanation.allowAccessState,denyPolicyExplanation.denyAccessState)'
}
check_database_iam rangle-stg-runtime@comin-up.iam.gserviceaccount.com rangle-stg-generation
check_database_iam rangle-stg-generation@comin-up.iam.gserviceaccount.com rangle-stg
```

Firestoreの `--resource-name` はconditionと同じ相対名で、`//firestore...` のfull nameとは異なる。他permissionはhelper第3引数へ `datastore.entities.list/create/update/delete` または `datastore.databases.get` を個別に渡す。正の対照はruntime→community、manager→generation。

`overallAccessState=CANNOT_ACCESS` だけをevaluatorの拒否判定とする。`UNKNOWN_INFO` / `UNKNOWN_CONDITIONAL` / HTTP403は拒否証拠にせず、実read/writeとも区別する。`testIamPermissions` は呼出し元の権限を返すためoperatorでは対象SAを検証できない。[caller scope](https://docs.cloud.google.com/resource-manager/reference/rest/v1/projects/testIamPermissions)、[context/権限](https://docs.cloud.google.com/policy-intelligence/docs/troubleshoot-access)、[v3判定値](https://docs.cloud.google.com/policy-intelligence/docs/reference/policytroubleshooter/rest/v3/iam/troubleshoot)

## 継続検証

`scripts/check-cloud-security.py` はaccount/projectを全commandへ明示するread-only metadata checker。最大40 command・全体180秒・1回30秒・JSON8MiBで、認証補助stderrは転載しない。

```bash
python3 scripts/check-cloud-security.py \
  --project=comin-up --account="$GCP_ACCOUNT" \
  --region=asia-northeast1 --service=rangle-stg \
  --runtime-service-account=rangle-stg-runtime@comin-up.iam.gserviceaccount.com \
  --service-account=rangle-stg-deploy@comin-up.iam.gserviceaccount.com \
  --service-account=rangle-stg-guard@comin-up.iam.gserviceaccount.com \
  --service-account=rangle-stg-scheduler@comin-up.iam.gserviceaccount.com \
  --service-account=rangle-stg-generation-sync@comin-up.iam.gserviceaccount.com \
  --service-account=rangle-stg-trainer@comin-up.iam.gserviceaccount.com \
  --service-account=rangle-stg-live-trainer@comin-up.iam.gserviceaccount.com \
  --bucket=rangle-stg-113704194486-catalog \
  --bucket=rangle-stg-113704194486-inputs \
  --bucket=rangle-stg-113704194486-media \
  --bucket=rangle-stg-113704194486-tfstate
```

| 検査 | 範囲 |
| --- | --- |
| project / Run | 既定Compute SAのbasic role不在、指定runtime SA、LB限定ingress |
| 明示SA | user-managed key不在 |
| 明示bucket | project所属、PAP/UBLA、projectViewer convenience/public grant不在。projectEditor/Owner・専用identityは対象外 |
| exit 0 / 1 / 2 | 全項目合格 / 設定指摘 / CLI失敗・timeout・不正metadataで未完了。出力はJSON |

全inventory/effective IAM、WAF、PITR/backup、signed URL、実データaccessは検証しない。API/workload追加時にはinventoryと必要権限を見直す。ローカルfake検証：

```bash
python3 scripts/check-cloud-security.test.py
```
