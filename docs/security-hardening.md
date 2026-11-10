# 公開環境の防御と復旧

正本は `infra/gcp/modules/presentation`。既存の[予算・期限・停止条件](stg-deployment.md#予算期限の停止)を保ち、通常の共有IAM grantは置き換えない。実適用と証拠は[調査記録](security-review.md)。

## Cloud Armor

| policy / 対象 | 制御 |
| --- | --- |
| `rangle-stg-app-security` / 通常・候補backend | 接続元IPごとにlogin/register合算30/分、他API書込120/分、API読取600/分。超過429 |
| app評価順 | メソッド制限 → API GET/HEADだけ感度1 `protocolattack-v33-stable` → rate。allowによるWAF迂回を防ぐ |
| `rangle-stg-media-security` / media・live-index backend bucket | GET/HEAD/OPTIONS、`/assets/`・`/manifests/`・`/generated/`・`/live/`・`/live-index/`・`/previews/`・`/ATTRIBUTION.txt` のみ許可。他path/root一覧は403 |

rateはbackend別の概算カウンターで、候補経路は別枠、共有IPの利用者は同じ枠になる。X-Forwarded-For/cookie/任意headerをキーにしない。書込本文にはWAFを適用せず、無害な `<script>text only` を含むJSONを拒否しない。入力検証・escape・認証はアプリが担う。媒体のRange/Originと世界向け匿名CORSは保持し、prefix追加時は許可条件も更新する。[rate](https://docs.cloud.google.com/armor/docs/rate-limiting-overview)、[WAF](https://docs.cloud.google.com/armor/docs/waf-rules)、[edge属性](https://docs.cloud.google.com/armor/docs/create-security-policy-rules#attributes)

`matches()` の選択肢は `(?:...)` とする。APIが拒否するcaptureを `python3 infra/gcp/modules/presentation/tests/security_policy_regex_test.py` で検出するが、mock/regex検査はCEL全体のAPI受理を証明しない。[noncapture公式例](https://docs.cloud.google.com/armor/docs/create-security-policy-rules#allow_or_deny_traffic_based_on_host_header)

edge policyはrate制限・本文WAFを提供せず、公開媒体の大量取得によるCDN/LB/egress課金を抑え切れない。署名URL/cookie・視聴者制限は製品判断を要する。[対応機能](https://docs.cloud.google.com/armor/docs/security-policy-overview)

Standardのservice-level policyだけを使う。2 policy・8 ruleの固定分は月換算USD18（時間按分）、global requestは100万件USD0.75、媒体/CDN等は別料金。domainなしのfoundationには作らない。[料金](https://cloud.google.com/armor/pricing)

停止guardはowned forwarding ruleの消失確認後、4 backendのowned policyを解除して2 policyを削除する。異なるattachmentは解除せず失敗記録にする。`setSecurityPolicy` / `setEdgeSecurityPolicy` へ空の `securityPolicy` を渡し、operationを有限回待つ。404は停止済みとする。[解除API](https://docs.cloud.google.com/armor/docs/manage-security-policies)

app/candidate backend serviceのget/setSecurityPolicyは名前条件付き。backend bucketとservice-level policyは名前条件の公式対応外のため、bucket get/setSecurityPolicy・policy get/deleteの4権限はguardだけのproject scope。固定対象リストとattachment照合で実行先を制限し、create/update/use/bucket deleteは付けない。[IAM対応範囲](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)

## Firestore の復旧

両named DB `rangle-stg` / `rangle-stg-generation` にPITR・日次7日保持backup・削除保護/`PREVENT`を設定する。PITR履歴は有効化後から蓄積し、backup復旧は初回完了後から可能。dailyの時刻指定はできない。[PITR](https://docs.cloud.google.com/firestore/native/docs/pitr)、[backup](https://docs.cloud.google.com/firestore/native/docs/backups)

停止guardは固定DB一覧のscheduleを名前・retention・recurrenceで検査して削除する。list/deleteはDB名IAM条件で絞れないためguard専用project custom roleと実行時検査で制限する。保存済backup・PITR・DBは残し、失敗しても他の停止を継続する。

storage/restoreは従量課金で、backupは同locationに残る（別regionの災害復旧ではない）。復元は既存DBを上書きせず新named DBへ行い、確認後に接続を切り替える。backupに含まれないTTL policy/IAMはTerraformから戻す。発表後のPITR・backup・保持データは別途判断する。

## 選択的な Data Access 監査

| service | 種類 | 対象 |
| --- | --- | --- |
| `storage.googleapis.com` | `ADMIN_READ`, `DATA_WRITE` | bucket設定参照、object作成/変更/削除 |
| `iam.googleapis.com` | `ADMIN_READ`, `DATA_READ` | IAM参照、token発行/署名 |
| `datastore.googleapis.com`（community/generation有効時） | `ADMIN_READ`, `DATA_WRITE` | Firestore管理参照・文書書込 |

IAM Credentialsは独立service設定ではなくIAM API、Firestore APIはdatastore設定で監査する。[IAM Credentials](https://docs.cloud.google.com/iam/docs/audit-logging/audit-logging-iamcreds)、[Firestore](https://docs.cloud.google.com/firestore/native/docs/audit-logging)

AuditConfigはproject全体の共有resourceにも効く。大量配信/readの費用を避けるためStorage/Firestoreの `DATA_READ` は有効にせず、読み取りによる持ち出しの全面監査はできない。Admin Activityは既定記録。[設定と費用](https://docs.cloud.google.com/logging/docs/audit/configure-data-access)

`google_project_iam_audit_config.security` は選択serviceのAuditConfigだけauthoritative。初回planで既存exemption/設定を確認し、必要ならimport/統合する。IAM全bindingは置き換えない。保存先は既存routing（通常 `_Default`）で、監査を除外するapp用observability bucketとは別。既存exclusion/retentionと増加log bytesを確認する。

## 適用後の確認

mockはattachment・順序/閾値・PITR/retention・監査対象を検査する。liveでは両app/両cache backend、通常視聴・Range/HEAD/OPTIONS・login・無害scriptコメント、403/429、PITR/schedule・監査ログを確認する。低samplingは全requestを記録しない。別DBの有料restore試験は設定確認と区別する。[実施結果](security-review.md)

停止時は各失敗を集約して後続を試し、失敗receipt・通知ackなし・guard自身pauseなしで再試行する。[停止順序](stg-deployment.md#予算期限の停止)
