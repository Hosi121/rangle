# 交流データの保存先

Go API のアカウント・セッション・いいね・コメントを、Tokyo の専用 `rangle-stg` Firestore Native / Standard に保存する。ブラウザは Go API の HttpOnly cookie を使う。

## 有効化

`application.community_enabled` は既定 `false`。Firestore 対応の [Go API（PR #45）](https://github.com/Hosi121/rangle/pull/45) を先に配備し、その後 `true` で Terraform を適用する。DB と IAM の設定後、Cloud Run に `-firestore-project comin-up -firestore-database rangle-stg` を追加する。CI は image / traffic、Terraform は DB / IAM / runtime args を管理する。

DB / API / TTL の管理権限は IaC 実行者が持ち、runtime SA には次の6権限だけを渡す。

- `datastore.databases.get`
- `datastore.entities.get` / `list` / `create` / `update` / `delete`

project IAM の条件を `resource.name == 'projects/comin-up/databases/rangle-stg'` と既存の期限に限定する。既存 DB を変更せず、鍵や匿名 IAM grant も追加しない。条件の実効は API / SDK で確認する（Cloud Console の表示では DB 条件が評価されない）。[IAM の公式仕様](https://docs.cloud.google.com/firestore/native/docs/manage-databases#configure_per-database_access_permissions)、[API 別権限](https://docs.cloud.google.com/firestore/native/docs/security/iam)。

## 保存と清掃

| path | 内容 |
| --- | --- |
| `accounts/{username}` | password hash と公開プロフィール |
| `sessions/{sha256_cookie}` | session と Timestamp 型の `expires_at` |
| `clips/{clip}/likes/{username}` | 利用者ごとのいいね |
| `clips/{clip}/comments/{id}` | 投稿者・本文・日時 |

件数は aggregation で取得する。`sessions.expires_at` に TTL と single-field index 免除を設定する。TTL は非同期清掃なので、Go 側でも期限を検証する。[TTL の仕様](https://docs.cloud.google.com/firestore/native/docs/ttl)。

DB は API / provider / Terraform の削除保護を有効にする。作成後に `community_enabled=false` や `application=null` へ戻すと削除保護で計画が止まるため、機能停止とデータ保持は明示的に分けて変更する。既存の予算・期限 guard は変更せず、公開停止後も DB を保持する。storage と TTL delete の費用は残る。named DB の無料枠は仮定せず、PITR / backup は追加しない。[料金](https://cloud.google.com/firestore/pricing)。

## 検証

Terraform 1.17.1 / Google provider 8.10.0 で fmt・validate・mock plan 計5ケースを確認済み。実 IAM の許可／他 DB の拒否、TTL 設定状態、実 query の確認は適用後に行う。TTL の実削除と大量アクセス時の性能・費用は未検証。
