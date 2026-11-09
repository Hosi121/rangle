# 既定 Compute サービスアカウントの権限整理

対象は `comin-up`（project number `113704194486`）の既定 Compute SA
`113704194486-compute@developer.gserviceaccount.com`。公開アプリの実行 SA
`rangle-stg-runtime@comin-up.iam.gserviceaccount.com` とは別の identity である。

## 2026-11-10 の変更前調査

既定 Compute SA に無条件の project-level `roles/editor` があった。調べた現在の
workload 設定に、この SA を使う依存先は見つからなかった。以下は設定メタデータの
調査結果であり、将来の workload や外部からの impersonation まで不使用を証明する
ものではない。アプリのデータ、Secret の値、ユーザー管理鍵の内容は読んでいない。

| 対象 | 確認結果 |
| --- | --- |
| Cloud Run services / jobs（project 全域） | service は Tokyo の `rangle-stg` のみ。専用 runtime SA を使用。jobs は 0 |
| Cloud Run の保持 revision | 75 件すべて専用 runtime SA を使用 |
| Compute instances / instance templates / managed instance groups / machine images（project 全域） | すべて 0 |
| Workflows（`--location=-`） | `rangle-stg-guard` のみ。専用 guard SA を使用 |
| Cloud Scheduler（取得できた全 34 location） | Tokyo の guard と generation-sync のみ。専用 scheduler / generation-sync SA を使用 |
| Vertex AI（応答する 45 regional endpoint） | Tokyo の過去の CustomJob 8 件のみ。trainer / live-trainer SA を使用。endpoint、pipeline、batch prediction、tuning、training pipeline、persistent resource は 0 |
| Vertex AI schedules | 40 region で 0。下記 5 region は location permission error |
| Discovery Engine data stores（global / us / eu） | すべて 0 |
| Cloud Functions / Cloud Build | API 無効。list は `SERVICE_DISABLED`。有効化や build は行っていない |
| GKE / Batch / Workbench / Notebooks | API 無効（有効 API 一覧で確認） |
| 既定 Compute SA の直接 IAM policy | binding なし |
| ユーザー管理 SA keys | 既定 Compute SA と rangle 専用 7 SA の全 8 identity で 0 |

Vertex の location 一覧には `asia-southeast3` も含まれたが、その regional endpoint
は調べた resource collection すべてで HTTP 404 だった。schedules の
`europe-west15`、`us-central2`、`me-central2`、`us-east7`、`us-west8` は
`Permission denied on locations/... (or it may not exist)` だった。これらを
「空」とは判定していない。上記以外の service の全設定、project 外の参照、削除済み
resource、将来の build の既定 identity は、この調査の保証範囲に含めない。

変更前は Data Access audit logging が無効だった。ログに使用記録がないことや
IAM Recommender の提案だけから必要権限を決めていない。Google も、既存 workload
への影響を評価してから既定 SA の Editor を整理するよう案内している。
[Compute Engine のサービスアカウント変更](https://docs.cloud.google.com/compute/docs/instances/change-service-account)

## 変更範囲と戻し方

この調査に基づく最小の変更は、project IAM の無条件 `roles/editor` binding から
既定 Compute SA の member だけを削除することである。代替ロールの必要性を示す
現在の workload は見つからなかったため、代替の project Viewer や Vertex User
を予防的には付けない。人の Editor / Owner、Google 管理 service agent、専用
runtime / trainer / guard の権限、既定 SA 自体の有効状態を維持する。

変更担当者は、直前の IAM policy を非公開の作業記録へ保存し、対象 member と条件が
一致することを再確認したうえで、次の 1 member の変更を行う。

```bash
gcloud projects remove-iam-policy-binding comin-up \
  --account="$GCP_ACCOUNT" --project=comin-up \
  --member=serviceAccount:113704194486-compute@developer.gserviceaccount.com \
  --role=roles/editor --condition=None
```

IAM の伝播後に下記 checker と既存の公開 HTTP smoke を実行する。新規の GPU job、
Workflow execution、Pub/Sub pull / publish を検証のために起動する必要はない。
既定 SA に起因する回帰を確認した場合の戻し方は、同じ command の
`remove-iam-policy-binding` を `add-iam-policy-binding` に変え、同じ member、role、
`--condition=None` を指定すること。project policy 全体の古い snapshot を set すると、
並行して加えた別の IAM 変更を失うので使用しない。

将来、既定 SA を使う依存先が見つかったら、その workload の設定と公式 API の必要
permission から専用 SA の権限を設計する。たとえば Storage object 権限は対象 bucket、
Artifact Registry 読み取りは対象 repository へ付与できる。Vertex pipeline は SA を
省略すると既定 Compute SA を使うので、専用 SA を明示する。pipeline 用の
`aiplatform.*` permission が必要になった場合も、その permission の対応 IAM scope
を確認し、未対応の resource-level binding を仮定しない。
[Vertex pipelines の identity と granular permissions](https://docs.cloud.google.com/vertex-ai/docs/pipelines/configure-project)

## 継続検証

`scripts/check-cloud-security.py` は `gcloud` の read-only metadata command のみを
使用する。account と project は必須で、全 command へ明示する。既定の gcloud
project や account には依存しない。最大 40 command、全体 180 秒、1 command 30 秒、
JSON 出力 8 MiB の上限を持ち、認証補助 tool の stderr を転載しない。

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

終了 code は、0 が全対象項目の合格、1 が検出した設定上の指摘、2 が CLI の失敗・
timeout・不正な metadata などで検証を完了できなかったことを示す。出力は JSON。
検証内容は、既定 Compute SA の project basic role 不在、指定 runtime SA、LB 限定
ingress、明示した SA のユーザー管理 key 不在、明示した bucket の所属 project、
public access prevention と uniform bucket-level access、projectViewer convenience
grant と public IAM grant 不在である。bucket の projectEditor /
projectOwner や専用 identity の権限は指摘対象にしない。

これは inventory 全体や IAM effective access の検証を再実行する tool ではない。
WAF、Firestore backup / PITR、signed URL、
runtime の実データ access は別に検証する。将来の API 有効化や workload 追加時は、
上記 inventory と必要権限の評価をやり直す。

ローカル検証は次で実行する。

```bash
python3 scripts/check-cloud-security.test.py
```
