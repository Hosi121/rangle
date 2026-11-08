# Community 用 Firestore の準備

Go API だけが Firestore に接続し、browser は同じ Go API の HttpOnly cookie を使う。既存 presentation の image / traffic は CI、runtime args / database / IAM は Terraform が管理する。今回の成果物は定義とローカル検証のみで、API 有効化・DB 作成・apply は行っていない。

## 有効化と接続契約

`application.community_enabled` は optional、既定 `false`。既存の image/catalog 設定だけでは Firestore API、database、TTL、追加 IAM を作らず、CLI 引数も増やさない。[Go API の flags 対応（PR #45）](https://github.com/Hosi121/rangle/pull/45) を含む server image を先に用意したうえで `true` にすると、`comin-up` の Tokyo `asia-northeast1` に専用の **`rangle-stg` / Firestore Native / Standard** を作る。既存 `(default)` や他 DB を import / 変更しない。

有効時の追加引数は `-firestore-project comin-up -firestore-database rangle-stg`。Go SDK は runtime SA の ADC を使い、鍵を渡さない。Cloud Run は DB 作成と runtime IAM の設定後に更新する。image は Terraform が ignore するため、未対応 image のまま新しい args を適用しない。初回の API 有効化・DB 作成・IAM 管理・TTL 設定は実行者の権限が別途必要で、CI deploy SA には追加しない。

## IAM の境界

Firestore の database 単位アクセスは、project allow policy の `resource.name == 'projects/comin-up/databases/rangle-stg'` 条件で設定する方式が公式に示されている。架空の database IAM resource は作らない。API / server SDK では条件が強制される一方、Google Cloud Console の DB 表示はこの条件で判定されないという公式の例外がある。条件の確認を Console 表示だけで済ませない。[Database access conditions](https://docs.cloud.google.com/firestore/native/docs/manage-databases#configure_per-database_access_permissions)

runtime SA に付ける custom role は `datastore.databases.get` と `datastore.entities.{get,list,create,update,delete}` の6権限。前者は begin/rollback transaction、後者は document CRUD・query・aggregation に対応する。DB / index / IAM の管理、他 DB への権限は追加しない。binding は DB 名に加え既存の `2026-11-14T15:00:00Z` で期限を切る。[API permission table](https://docs.cloud.google.com/firestore/native/docs/security/iam)

IAM は runtime SA 全体の権限で、個々の利用者の認証・本人確認は Go API の責務。password hash / account / session を public bucket、CDN、catalog、ログへ出さず、browser の Firestore SDK、直接アクセス用資格情報、匿名 IAM grant は作らない。広い既存 IAM が別途付いていれば allow policy は加算されるため、実運用前に runtime SA の権限全体と別 DB への拒否を SDK/REST で確認する。Firestore IAM の反映には最大約5分かかる場合がある。

## データと TTL

| path | 役割 |
| --- | --- |
| `accounts/{normalized_username}` | 一意な利用者名と password hash 等の private account |
| `sessions/{sha256_cookie}` | 生の cookie を保存しない session、`expires_at` は Firestore Timestamp |
| `clips/{clip}/likes/{username}` | 利用者単位の like |
| `clips/{clip}/comments/{id}` | 投稿者と本文等の comment |

件数は aggregation を使い、全員が更新する共通 counter document を作らない。この IaC は空の DB と TTL field 設定だけを作り、実ユーザーや sample document は挿入しない。実 query に複合 index が必要になれば、その query を根拠に追加する。

`sessions` collection group の `expires_at` に TTL を設定し、この timestamp の single-field index は免除する。Go は期限切れを毎回判定し、TTL 削除をログイン失効の条件にしない。清掃は非同期で通常は期限後24時間以内、policy の有効化にも10分以上かかり得る。Timestamp 以外や欠落 field は清掃対象にならず、子 collection の連鎖削除も行わない。[TTL policy](https://docs.cloud.google.com/firestore/native/docs/ttl)

## 保持・費用・検証

DB は API delete protection、provider `deletion_policy=PREVENT`、Terraform `prevent_destroy` を設定する。一度有効化した DB を持つ状態で `community_enabled=false` や `application=null` にすると、DB 削除を計画するため保護で停止する。機能停止とデータ保持を分け、保持対象を確認した明示変更で扱う。期限後も account / comment / like は残し、session の TTL 清掃は続く。既存 USD300 予算、JPY200 Cloud Run cap、期限/budget guard は変更せず、DB を guard の自動削除先に加えない。

Firestore は document 操作、index 読取り、storage、network を課金し、TTL delete は無料枠除外。PITR / backup は追加しない。保存量が小さくても期限後の storage / TTL delete 費用は残り、named DB に無料枠が割り当てられるとは仮定しない。削除保護は backup の代わりではなく、data の保持・退避・削除は公開サービス停止後に確認する。[Firestore pricing](https://cloud.google.com/firestore/pricing)

Terraform 1.17.1 / Google 8.10.0 の provider schema と公式 resource 例で database / field の設定を確認した。fmt、init `-backend=false`（provider 取得のみ）、module/root validate は成功。default-off / enabled の追加2ケースと既存3ケースの mock plan 計5ケースも成功した。実 SDK の認証・IAM 条件の許可/拒否・TTL ACTIVE / 実削除、料金、query index の実行は未検証。親が Go API と統合レビューした後に実設定と配備を判断する。
