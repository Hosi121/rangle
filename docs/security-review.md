# 2026-11-10 クラウドセキュリティ調査と対応

`Hosi121/rangle` stg / GCP `comin-up` のコード・Terraform・実IAM/サービス設定と限定HTTP確認を照合した。既存データ・予算・期限を保持し、新GPU job・破壊的停止試験は行っていない。

## 指摘と変更

| 指摘 | 対応・確認結果 |
| --- | --- |
| 公開runtimeに生成DB/Vertex/actAs | IAM必須のmanager/SA/DBへ分離。生成10文書を厳密照合し、候補から過去生成4件のdetail/交流200 |
| `run.app` でLB迂回 | LB限定ingress（PR #116）。直通404、独自domain UI/API 200 |
| appのWAF/rateなし | 通常/候補へArmor。認証30/分・他書込120/分・読取600/分を設定/mock照合。禁止メソッド403とpolicy/priority100/DENYをLBログ確認。rate限界試験なし |
| mediaの一覧/別namespace公開 | 両backend bucketへedge policy。root403・公開object200・既存smoke成功 |
| 既定Compute SAにEditor | workload metadata調査後、その1 grantだけ削除。人/専用SAは維持 |
| 4 bucketのprojectViewer grant | legacyBucketReader/legacyObjectReaderの対象memberだけ除去。移行前checker 23/23合格 |
| DB復旧履歴なし | 両DBのPITR・日次7日backupを実API照合。データ/削除保護を保持 |
| Data Access監査なし | Firestore/Storage管理参照・書込、IAM管理参照・資格情報読取を有効化。実Firestore Commitの監査ログ確認 |
| guardの途中失敗で後続中断 | 失敗集約・後続停止/GPU取消、manager/Armor/backup schedule停止を追加。offline25件・Terraform/Workflows実compile。削除分岐未実行 |
| 公開読取負荷/管理者password強度 | handler別上限・新規/変更時15文字以上。Go race/vet・認証/降格/負荷回帰検証。既存passwordの強制変更なし |

設定・費用・IAM scopeは[防御と復旧](security-hardening.md)、調査の網羅範囲は[identity記録](security-identity.md)、切替/復帰契約は[台帳移行](security-generation-migration.md)。

## データを保持した切替

1. 通常/候補の生成APIを遮断し、sync Schedulerをpause。8 jobすべてterminal、active空、**used=8**を確認した。
2. 生成2 collectionだけをprivate snapshot/GCSへ新規作成限定で退避し、読み戻しSHAを照合した。
3. 8 job＋2 controlを1 atomic commitでコピーし、native fields一致・source無変更を再確認した。owner/preset/history/掲載aggregate/usedをresetせず、旧DB/collectionも残した。
4. manager healthは匿名403・運用者200、application service認証のない業務APIは401。公開runtime実ID tokenの候補でUI/API・過去非live生成4件のdetail/交流を確認してtrafficを切り替えた。専用Scheduler→managerのOIDC syncは200。

## 残る制約

- 公開runtime侵害時はcommunity accessとowner偽装による残枠内の正規操作が可能。managerは固定spec・同時1・最大10 attempts・30分・期限を守るが、公開RCEを防止しない。
- 公開媒体の大量取得費用とBudget通知遅延は残り、guardは厳密なhard capではない。現Google accountにBudget一覧権限がなく再読取できなかったため、承認予算USD300と既存期限・Run capを維持した。
- PITR/scheduleの設定確認はrestore成功を示さない。別DB restore・実削除停止試験は未実施。
- 実IAM宣言と通信は確認したが、Troubleshooterは `SERVICE_DISABLED`。対象SAの拒否証拠でも、全service/将来workload/project外の無権限証明でもない。
- Storage/Firestore `DATA_READ` は無効で、読取持ち出しの全面監査は提供しない。
