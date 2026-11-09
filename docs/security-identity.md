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

## 適用結果（2026-11-10、private generation manager 追加前）

変更担当者が live 環境で次の変更と検証を行った。

- project の無条件 `roles/editor` から既定 Compute SA の member だけを削除した。
  代替ロールは付けず、人の Editor / Owner や別の identity の binding は維持した。
- `rangle-stg-113704194486-{catalog,inputs,media,tfstate}` の 4 bucket から、
  `projectViewer:comin-up` に対する `roles/storage.legacyBucketReader` と
  `roles/storage.legacyObjectReader` の 2 grant ずつを削除した。projectEditor /
  projectOwner の convenience owner と専用 runtime / operator の権限は維持した。
- 下記 checker を専用 7 SA・既定 Compute SA・4 bucket に実行し、23/23 項目合格、
  終了 code 0 を確認した。basic role 不在、runtime identity、LB 限定 ingress、
  8 identity のユーザー管理 key 不在、各 bucket の所属・PAP・uniform access・
  viewer convenience grant / public IAM grant 不在の検証である。

この記録は private generation manager と専用 database の追加前の結果であり、
それらの追加後の権限分離や実データへの拒否を検証した記録ではない。

## 変更範囲と戻し方

この調査に基づく最小の変更は、project IAM の無条件 `roles/editor` binding から
既定 Compute SA の member だけを削除することである。代替ロールの必要性を示す
現在の workload は見つからなかったため、代替の project Viewer や Vertex User
を予防的には付けない。人の Editor / Owner、Google 管理 service agent、専用
runtime / trainer / guard の権限、既定 SA 自体の有効状態を維持する。

対象の 1 member を削除する command は次のとおり。再適用や復旧時も、直前の IAM
policy を非公開の作業記録へ保存し、対象 member と条件の一致を確認する。

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

## IAM evaluator の確認範囲

2026-11-10 に runtime → Vertex create、runtime → generation database の
`datastore.entities.get`、manager → community database の同 permission を、
Policy Troubleshooter v3 へ現在時刻の condition context を付けて問い合わせた。
3 件とも HTTP 403、`PERMISSION_DENIED`、reason `SERVICE_DISABLED`、
service `policytroubleshooter.googleapis.com`、consumer `projects/comin-up` だった。
これは evaluator を利用できない結果であり、対象 SA が拒否された証拠ではない。
Cloud Asset API も有効 API 一覧に含まれず、Policy Analyzer も未実行である。
同 Analyzer は allow policy の分析で、反映が best effort のため、切替直後の空結果を
拒否の証拠にしない。
[Policy Analyzer の前提と反映の制約](https://docs.cloud.google.com/policy-intelligence/docs/analyze-iam-policies)
API 有効化、IAM 付与、SA impersonation、アプリデータ access は行っていない。

以下は Troubleshooter API が既に有効で、必要な policy / custom role の閲覧権限がある
場合の再確認用。`CHECK_TIME` は確認時点の UTC RFC3339 時刻を指定する。IAM の global
API には `--region` がなく、Vertex create の project-level permission を評価する。
Tokyo の API 呼出しや resource 作成は発生しない。

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

Firestore の `--resource-name` は既存 condition と同じ相対名で、`//firestore...` の
full resource name と区別する。他の権限も確認する場合は helper の第 3 引数へ
`datastore.entities.list`、`datastore.entities.create`、`datastore.entities.update`、
`datastore.entities.delete`、`datastore.databases.get` を個別に渡す。
正の対照は runtime → community、manager → generation の組合せ。

`overallAccessState=CANNOT_ACCESS` が evaluator の拒否判定であり、`UNKNOWN_INFO` /
`UNKNOWN_CONDITIONAL` や HTTP 403 は拒否の証明に使わない。判定は現在の policy と
指定 context に対する v3 の IAM allow / deny 評価で、実データの read/write や API 全体の挙動の確認とは
分けて報告する。`testIamPermissions` は呼出し元の権限を返すため、operator で実行しても
対象 SA の拒否は確認できない。
[testIamPermissions の呼出し元 scope](https://docs.cloud.google.com/resource-manager/reference/rest/v1/projects/testIamPermissions)
[Troubleshooter の必要権限と条件 context](https://docs.cloud.google.com/policy-intelligence/docs/troubleshoot-access)、
[v3 の判定値](https://docs.cloud.google.com/policy-intelligence/docs/reference/policytroubleshooter/rest/v3/iam/troubleshoot)

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
