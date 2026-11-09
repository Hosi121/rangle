# 生成台帳を専用 Firestore database へ移す

`scripts/migrate-generation-ledger.py` は `comin-up` の `rangle-stg` から
`rangle-stg-generation` へ、`generation_jobs` と `generation_control` だけをコピーする。
project、account、source、target はコード内の固定値と CLI の `choices` で制限する。
別 project を指す gcloud の既定設定は使わない。account は
`takuyakataiwa@gmail.com` に固定する。

generation job は最大 10 件、ID は小文字 32 桁 hex のみ。control は最大 2 件で
`admission` と `public_feed` だけを許可する。accounts、sessions、clips、下位
collection、任意の document path は読み書きしない。database 作成、IAM 変更、
job の起動・取消、Pub/Sub 操作は行わない。

## 事前条件

操作担当者が source と target への生成受付、状態同期、公開状態の変更を停止する。
source の job はすべて `succeeded`、`failed`、`cancelled` のいずれかでなければならない。
admission がある場合は native Firestore 値が `active: {stringValue: ""}` であり、
`used: {integerValue: "0".."10"}` であることを要求する。job があるのに admission
がない場合も拒否する。全 collection が空の場合は有効で、台帳を作ったり予算を
リセットしたりしない。

既存 document の native `fields` は変換せずコピーする。整数の文字列表現、timestamp、
map、array、bytes、reference、public_feed の aggregate と attempt counter を保つ。
referenceValue の参照先も書き換えない。document ID は同じにし、Firestore が管理する
createTime / updateTime は target で新しい値になる。

バックアップの配置先は、操作担当者が事前に作った所有者本人だけがアクセスできる
directory とする（例: mode0700）。作業リポジトリへ snapshot を保存・commit しない。
`--backup` は絶対 path が必須で、utility は親 directory を作らない。

## 操作

1. `--prepare-backup` で source だけを読み、exclusive creation・mode0600 で snapshot
   を作る。同じ path の既存ファイルや symlink は上書きしない。target が未作成でも実行できる。
2. 操作担当者が target database と必要な権限を IaC で用意する。snapshot は private GCS
   に退避し、hash と保存先を非公開の運用記録へ残す。公開 runtime を切り替える前に
   durable backup を確認する。
3. 通常の dry-run でコピー計画を確認する。cloud write とローカル file 作成はない。
4. `--apply` に snapshot の SHA256 を渡す。同じ source と backup であることを確認し、
   不足 document だけを一度の atomic commit で作る。
5. `migration_verified` を確認し、操作担当者が runtime の切替と既存 HTTP smoke を行う。
   旧 database の削除や旧 collection の削除はこの utility の範囲に含めない。

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

出力は status、件数、SHA256 のみ。job や control の内容は表示しない。
`--prepare-backup` が出す `backup_sha256` は、改行を含む実ファイル全体の SHA256。
`source_sha256` は source の document 名・native fields・createTime・updateTime、
`content_sha256` は database 名と server metadata を除いた相対 ID と native fields の hash。
終了 code 0 はその mode の処理成功、1 は拒否または検証失敗、2 は CLI 引数エラーである。

既存 target document が一致すれば skip し、内容が違う場合や source にない target
document がある場合は拒否する。新規 document の write は全件
`currentDocument.exists=false` を付けるため、競合時も上書きしない。commit の自動 retry
はない。不明な通信失敗後は writers を停止したまま、同じ backup と digest で再実行すると、
すでに一致した document を skip できる。
[Firestore atomic commit](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/projects.databases.documents/commit) と
[write precondition](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/Write)

source を commit 直前と直後に再取得し、backup 準備時から fields と metadata が変わって
いないことを確認する。target も再取得し、native fields の厳密な一致を確認する。
database をまたぐ transaction ではないため、source / target の writers を停止する事前条件を
代替しない。commit 後の drift・検証失敗では target の削除や巻き戻しをせず、
`do_not_switch_runtime` を含む status で停止する。

REST は固定の `https://firestore.googleapis.com` のみで、redirect を拒否する。
認証 token は明示した account / project の `gcloud auth print-access-token` から
process 内へ捕捉し、file、子 process の argv、出力へ渡さない。API response や SDK の
stderr もエラーに転載しない。REST は最大 20 回・全体 180 秒・1 回 20 秒、response と
backup は 16 MiB、commit request は 10 MiB の上限で拒否する。collection の
pagination が必要な場合も、そのまま続行しない。
[Firestore の bounded collection list](https://docs.cloud.google.com/firestore/docs/reference/rest/v1/projects.databases.documents/list)

検証は実データや cloud apply を使わず、fake REST transport で行う。

```bash
python3 scripts/migrate-generation-ledger.test.py
```
