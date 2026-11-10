# 交流データの保存先

アカウント・セッション・いいね・声援・コメントはTokyoのFirestore Native/Standard `rangle-stg` へ保存する。ブラウザはGo APIのHttpOnly cookie、[生成台帳](generation-infrastructure.md)は別DBを使う。

## 有効化

`application.community_enabled` は既定 `false`。[対応Go API（PR #45）](https://github.com/Hosi121/rangle/pull/45)を先に配備し、TerraformでDB/IAMと `-firestore-project comin-up -firestore-database rangle-stg` を設定する。image/trafficはCIが管理する。

runtimeには `datastore.databases.get` と `datastore.entities.{get,list,create,update,delete}` だけを渡し、条件を `resource.name == 'projects/comin-up/databases/rangle-stg'` と既存期限に限定する。DB/API/TTL管理はIaC実行者の権限。既存DB・匿名grant・鍵は追加変更しない。条件の実効はConsole表示でなくAPI/SDKで確認する。[DB別IAM](https://docs.cloud.google.com/firestore/native/docs/manage-databases#configure_per-database_access_permissions)、[API別権限](https://docs.cloud.google.com/firestore/native/docs/security/iam)

## 保存と清掃

| path | 内容 |
| --- | --- |
| `accounts/{username}` | password hash・公開プロフィール |
| `accounts/{username}/cheered_clips/{clip}` | 声援受付時の動画ラベル・最後の送信日時（本人専用） |
| `clips/{clip}/cheer_receipts/{id}` | 再送用receipt（`expires_at` は受付の7日後） |
| `clips/{clip}/cheer_shards/{shard}` | 声援の48区間集計、32shard |
| `publisher_accounts/{account}` | 団体の公開プロフィール・内部状態 |
| `publisher_accounts/{account}/staff_accounts/{staff}` | 団体内担当者・password hash・資格情報世代 |
| `publisher_accounts/{account}/staff_names/{username}` | 団体内の担当者名からIDへの一意索引 |
| `staff_sessions/{sha256_cookie}` | 担当者principal・資格情報世代・`expires_at` |
| `sessions/{sha256_cookie}` | session・Timestamp型 `expires_at` |
| `clips/{clip}/likes/{username}` | 利用者ごとのいいね |
| `clips/{clip}/comments/{id}` | 投稿者・本文・日時 |

件数はaggregationで取得する。`sessions.expires_at` はTTLとsingle-field index免除を設定する。TTLは非同期のためGoも期限を検証する。[TTL](https://docs.cloud.google.com/firestore/native/docs/ttl)

DBはAPI/provider/Terraformの削除保護を持ち、`community_enabled=false` / `application=null` では計画が止まる。機能停止と保持を分け、停止後も保存/TTL delete費用を見込む。named DB無料枠は仮定しない。[料金](https://cloud.google.com/firestore/pricing)。2026-11-10に追加したPITR・日次backupは[防御と復旧](security-hardening.md#firestore-の復旧)を参照する。

## 検証

Terraform 1.17.1 / Google provider 8.10.0でfmt・validate・mock plan 5ケースを確認。適用後のPITR等の証拠は[調査記録](security-review.md)。他DBへの実アクセス拒否、TTL実削除、大量アクセスの性能・費用は未検証。
