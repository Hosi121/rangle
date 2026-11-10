# 生成台帳を専用 Firestore database へ移す

`scripts/migrate-generation-ledger.py` は `comin-up` / `takuyakataiwa@gmail.com` 固定で、`rangle-stg` → `rangle-stg-generation` の `generation_jobs` / `generation_control` だけをコピーする。project/account/DBはコードとCLI choicesで制限し、gcloud既定値は使わない。

対象は最大10 job（小文字32桁hex ID）、controlは `admission` / `public_feed` の最大2件。accounts/sessions/clips・下位collection・任意pathは読まず、DB作成/IAM/job起動取消/PubSub操作もしない。実施結果は[2026-11-10切替記録](security-review.md#データを保持した切替)。

## 事前条件

- 両DBの生成受付・状態同期・掲載変更を止める。全jobが `succeeded` / `failed` / `cancelled`、admissionがあればnative値の `active: {stringValue: ""}`、`used: {integerValue: "0".."10"}` を要求する。jobあり・admissionなしは拒否する。全件空でも台帳/予算を新規作成・resetしない。
- native `fields`・IDを変換せず、整数文字列/timestamp/map/array/bytes/reference・aggregate/attemptを保つ。referenceValueも書き換えない。targetのcreateTime/updateTimeだけは新しくなる。
- backupは事前作成した所有者専用directory（例mode0700）の絶対pathへ保存する。親directoryは作らず、repositoryへ保存/commitしない。

## 操作

1. `--prepare-backup` でsourceだけを読み、exclusive creation・mode0600でsnapshotを作る。既存file/symlinkは上書きせず、target未作成でも実行できる。
2. IaCでtargetと権限を用意する。snapshotをprivate GCSへ退避し、hash/保存先を非公開記録へ残す。runtime切替前にdurable backupを確認する。
3. dry-runで計画を確認する（cloud write・file作成なし）。
4. `--apply` にbackup SHA256を渡し、source一致を確認して不足文書だけを1 atomic commitで作る。
5. `migration_verified` 後にruntime切替・既存HTTP smokeを行う。旧DB/collectionは削除しない。

```bash
python3 scripts/migrate-generation-ledger.py \
  --project=comin-up --account=takuyakataiwa@gmail.com \
  --source=rangle-stg --target=rangle-stg-generation \
  --backup="$PRIVATE_BACKUP_DIRECTORY/generation-ledger.json" --prepare-backup

python3 scripts/migrate-generation-ledger.py \
  --project=comin-up --account=takuyakataiwa@gmail.com \
  --source=rangle-stg --target=rangle-stg-generation \
  --backup="$PRIVATE_BACKUP_DIRECTORY/generation-ledger.json"

python3 scripts/migrate-generation-ledger.py \
  --project=comin-up --account=takuyakataiwa@gmail.com \
  --source=rangle-stg --target=rangle-stg-generation \
  --backup="$PRIVATE_BACKUP_DIRECTORY/generation-ledger.json" \
  --apply --backup-sha256="$PREPARED_BACKUP_SHA256"
```

| 出力 | 意味 |
| --- | --- |
| `backup_sha256` | 改行を含むbackup実ファイル全体 |
| `source_sha256` | source文書名・native fields・createTime/updateTime |
| `content_sha256` | DB名/server metadataを除く相対ID・native fields |
| exit 0 / 1 / 2 | mode成功 / 拒否・検証失敗 / CLI引数エラー |

出力はstatus/件数/SHAだけで、job/control本文は表示しない。targetの一致文書はskipし、不一致・sourceにない文書は拒否する。新規writeは全件 `currentDocument.exists=false` とし、上書き・自動retryはしない。不明な通信失敗後はwritersを止めたまま同じbackup/digestで再実行し、一致文書をskipできる。[atomic commit](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/projects.databases.documents/commit)、[precondition](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/Write)

commit前後にsource fields/metadataの無変化とtarget fieldsの厳密一致を確認する。DB間transactionではないためwriters停止は必須。commit後のdrift/失敗はtargetを消さず `do_not_switch_runtime` で停止する。

## 切替後の正本と復帰

生成の正本は `rangle-stg-generation`、communityは `rangle-stg`。CIの復帰はmanager/公開appのrevision・trafficだけで、DB/IAM/used/active/履歴/掲載は戻さない。古いbackupや空DBでattemptを再発行しない。

初回失敗で旧monolithへtrafficが戻っても旧生成権限を復活させない。targetへ新規writeがあればsourceは古いため、両writersと同期を止めて差分を確認し、互換remote版で再検証する。utilityは逆コピー・target削除・自動restoreをしない。移行失敗時は切替せず、backupと両DBを保持する。

RESTは `https://firestore.googleapis.com` 固定、redirect拒否。tokenは明示account/projectの `gcloud auth print-access-token` からprocess内だけで捕捉し、file/argv/出力へ渡さない。API本文・SDK stderrも転載しない。上限はREST20回・全体180秒・1回20秒、response/backup16MiB、commit10MiB。paginationが必要なら拒否する。[bounded list](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/projects.databases.documents/list)

実データ/cloud writeを使わないfake REST検証：

```bash
python3 scripts/migrate-generation-ledger.test.py
```
