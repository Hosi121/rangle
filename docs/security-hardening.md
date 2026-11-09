# 公開環境の防御と復旧

発表用環境の期限 `2026-11-14T15:00:00Z`、総額 USD300、Cloud Run の min=0 / max=1 と停止 guard を維持する。ここで追加する設定は `infra/gcp/modules/presentation` が管理し、通常の共有 IAM grant を置き換えない。

## Cloud Armor

`rangle-stg-app-security` は通常経路と `X-Rangle-Candidate` 経路の両方へ付ける。接続元 IP ごと・backend ごとのおおよその制限は、login/register 合算で30件/分、その他 API 書き込み120件/分、API 読み取り600件/分。超過は429。IPを共有する利用者は枠も共有する。`X-Forwarded-For`、cookie、ユーザー指定 header をレートキーにしない。候補経路は独立 backend なので通常経路と別のカウンターになる。[レート制限の仕様](https://docs.cloud.google.com/armor/docs/rate-limiting-overview)

メソッド制限と、API の GET/HEAD に限る感度1の `protocolattack-v33-stable` を先に評価する。レートルールの `allow` で WAF を迂回しない順序にする。POST/PUT/DELETE のコメント本文やパスワードには WAF を適用しないため、無害な `<script>text only` を含む JSON をブロックする原因にしない。書き込みの検証・出力時のエスケープ・認証はアプリの責務として残る。[WAF ルール](https://docs.cloud.google.com/armor/docs/waf-rules)

`rangle-stg-media-security` は媒体と live-index の両 backend bucket へ付ける。GET/HEAD/OPTIONS と、現在の配信契約の `/assets/`、`/manifests/`、`/generated/`、`/live/`、`/live-index/`、`/previews/`、root object `/ATTRIBUTION.txt` だけを許可する。bucket root や他の object 名前空間は403。Range と Origin は制限せず、世界中の匿名視聴と CORS を維持する。公開 prefix を増やす際には、この許可条件も更新する。[edge policy の属性](https://docs.cloud.google.com/armor/docs/create-security-policy-rules#attributes)

backend bucket 用の edge policy はレート制限・本文 WAF を提供しない。許可済みの大きな媒体を繰り返し取得する攻撃の課金を、この設定だけで抑え切ることはできない。署名 URL/cookie、視聴者制限、媒体配信構成の変更は公開視聴の製品要件に関わるため、別の判断が必要。[policy の対応機能](https://docs.cloud.google.com/armor/docs/security-policy-overview)

Standard のサービス単位 policy だけを使い、Enterprise や hierarchical policy を作らない。policy 2個と rule 8個なら固定分は月換算 USD18（時間単位で按分）、global request は100万件あたり USD0.75。媒体 CDN/LB/egress の既存課金は別途発生する。domain 未設定の foundation には Armor policy を作らない。停止 guard は owned forwarding rules の消失を確認した後、上記4 backend の owned policy を解除し、上記2 policy を削除して固定費を止める。異なる policy が付いていれば解除せず失敗記録を残す。削除失敗時は guard を継続させるため、記録を確認して解消する。[公式料金](https://cloud.google.com/armor/pricing)

解除 API は backend service の `setSecurityPolicy`、bucket の `setEdgeSecurityPolicy` に空の `securityPolicy` を渡す。操作完了を有限回待ち、404 は既に停止済みとして扱う。guard は生成管理サービス `rangle-stg-generation` を公開 Run service より先に削除する。Armor・manager・公開 Run のいずれかで失敗しても後続の停止と GPU cancellation を試み、失敗 receipt を残し、budget 通知の ack と guard 自身の pause を行わない。[解除仕様](https://docs.cloud.google.com/armor/docs/manage-security-policies)

backend service の get/setSecurityPolicy 権限は owned app/candidate の `resource.name` 条件で制限する。backend bucket と service-level security policy は公式の名前条件対応表に載らないため、bucket get/setSecurityPolicy と policy get/delete の4権限は guard のみに project scope で付ける。これは IAM による対象名の制限ではなく、固定対象リストと既存 attachment の所有確認による実行範囲の制限。policy create/update/use、bucket delete は付けない。[IAM resource attribute の対応範囲](https://docs.cloud.google.com/iam/docs/conditions-resource-attributes)

## Firestore の復旧

既存の named database `rangle-stg` で PITR を有効にし、毎日の backup を7日間保持する。削除保護と `PREVENT` は維持する。PITR の履歴は有効化以後から蓄積され、過去7日分が即座に作られるわけではない。daily backup の時刻は指定できず、初回 backup 完了まで backup による復旧はできない。[PITR](https://docs.cloud.google.com/firestore/native/docs/pitr)、[scheduled backup](https://docs.cloud.google.com/firestore/native/docs/backups)

PITR/backup storage と実際の restore は従量課金。小さい database 向けの短い retention に限定する。backup は同じ location に置かれ、元 DB を削除しても保持期限まで残る。これは別リージョンの災害復旧ではない。既存データを上書きせず、新しい named database へ復元して検証後に接続先を切り替える。backup に含まれない TTL policy と必要な IAM は Terraform から戻す。定期 backup の停止、PITR、保持データの発表後の扱いも、データ保持の判断と一緒に確認する。

## 選択的な Data Access 監査

| AuditConfig の service | 有効にする種類 | 目的 |
| --- | --- | --- |
| `storage.googleapis.com` | `ADMIN_READ`, `DATA_WRITE` | bucket設定参照、object作成・変更・削除 |
| `iam.googleapis.com` | `ADMIN_READ`, `DATA_READ` | IAM参照、service account token発行・署名 |
| `datastore.googleapis.com`（communityまたはgeneration有効時） | `ADMIN_READ`, `DATA_WRITE` | Firestore管理参照、文書書き込み |

IAM Credentials の Data Access は独立した `iamcredentials.googleapis.com` 設定では有効化できず、IAM API の設定が必要。Firestore は `datastore.googleapis.com` の設定が Firestore API も対象にする。[IAM Credentials 監査](https://docs.cloud.google.com/iam/docs/audit-logging/audit-logging-iamcreds)、[Firestore 監査](https://docs.cloud.google.com/firestore/native/docs/audit-logging)

AuditConfig は project 全体で効くため、共有リソースの該当操作も記録する。公開 CDN 配信や私有 catalog の大量読み取りを含む Storage の `DATA_READ`、公開 interaction 読み取りを含む Firestore の `DATA_READ` は有効化しない。このため、object/文書の読み取りによる持ち出しを全面的に追跡できる設定ではない。Admin Activity は既定で記録される。[Data Access の設定と費用上の注意](https://docs.cloud.google.com/logging/docs/audit/configure-data-access)

`google_project_iam_audit_config.security` は選択した service の AuditConfig のみ authoritative。初回 plan では既存の exemption/設定を確認し、変更があれば import・統合してから適用する。IAM binding 全体を authoritative にしない。Data Access の保存先は既存 Logging routing（通常 `_Default`）であり、監査ログを除外するアプリ用の observability bucket/dashboard へ設定を追加するだけでは、監査は有効にならない。運用時は `_Default` の exclusion と retention、増えた log bytes を確認する。

## 適用後の確認

Terraform の mock test は policy attachment、ルール順序と閾値、PITR、backup retention、監査対象を検証する。Cloud Armor の CEL を API が受理することや実際の traffic 判定までは保証しない。実環境では plan を確認して適用し、両経路・両 cache backend の policy attachment を取得し、通常視聴、Range、HEAD、OPTIONS、login、無害な script 文字列を含むコメントを確認する。非公開 path の403、レート超過の429、backup schedule と PITR 状態、該当サービスの Data Access 記録も確認する。既存の低い LB sampling のままでは全リクエストを記録できない。復元テストは別 DB と追加料金を伴うため、実施結果を設定確認と区別して記録する。
