# 2026-11-10 クラウドセキュリティ調査と対応

対象は `Hosi121/rangle` の stg と GCP project `comin-up`。リポジトリ、Terraform、実 IAM・サービス設定、限定した HTTP 確認を突き合わせたホワイトボックス調査である。既存データと予算・停止期限を保持し、新しい GPU ジョブや破壊を伴う停止試験は実行していない。

## 指摘と変更

| 指摘 | 対応 | 確認したこと |
| --- | --- | --- |
| 公開 runtime が生成台帳を変更でき、Vertex 操作・trainer actAs も持つ | IAM 必須の専用 Cloud Run・SA・Firestore へ生成制御を分離。公開 runtime は固定 API の invoke のみ | 新旧 DB の生成文書10件を厳密照合。候補経路から過去の静的・動的生成4件の詳細・交流情報が200 |
| `run.app` から LB の防御を迂回できる | 公開サービスを LB 限定 ingress に変更（PR #116） | 直通 URL は404、独自ドメインの UI/API は200 |
| エッジのレート制限・WAF がない | 通常・候補 backend に Cloud Armor。認証30/分、その他書込120/分、読取600/分を接続元 IP ごとに制限 | 両 backend の禁止メソッドが403。LB ログで policy・priority100・DENY を確認。閾値は実設定と mock test で確認し、大量リクエストによる限界試験は行っていない |
| 媒体 CDN からバケット一覧や別名前空間へアクセスできる | 両 backend bucket に edge policy。公開 prefix と GET/HEAD/OPTIONS のみ許可 | root 一覧は403、公開オブジェクトは200。既存配信 smoke を通過 |
| 既定 Compute SA に無条件 Editor がある | 全域の workload メタデータを確認し、対象 SA の1 grantだけ除去 | 変更後 checker による不在確認。人の Owner/Editor・専用 SA の権限を保持 |
| 4バケットに projectViewer convenience grant がある | 各バケットの legacyBucketReader / legacyObjectReader から対象 member だけ除去 | private/PAP/UBLA・public IAM 不在を含め、移行前23項目すべて合格 |
| Firestore の復旧履歴と定期バックアップがない | community / generation の両 DB に PITR と日次7日保持の backup schedule | 実 API で PITR・schedule・retention を確認。既存データを削除しない設定を維持 |
| Data Access の監査が無効 | Firestore/Storage の管理参照・書込、IAM の管理参照・資格情報関連読取を有効化 | 実際の Firestore Commit が Data Access ログへ記録されたことを確認 |
| 停止 guard の途中失敗で後続停止が中断する | 対象ごとに失敗を集約し、後続停止・GPU取消を継続。manager、Armor、owned backup schedule も停止対象に追加 | offline 分岐検証25件、Terraform と Workflows の実コンパイル。破壊を伴う停止分岐は未実行 |
| 公開読取の負荷・管理者向け新規パスワード強度が不足 | handler ごとの読取上限、新規・変更時の管理者パスワード最小15文字を追加 | Go race test・vet、認証・降格・負荷制御の回帰検証。既存パスワードの強制変更は行っていない |

設定・費用・実 IAM の制約は [防御と復旧](security-hardening.md)、権限整理の調査範囲は [identity 記録](security-identity.md)、移行と復帰は [台帳移行](security-generation-migration.md) に記載する。

## データを保持した切替

生成 API を通常・候補の両経路で一時遮断し、専用同期 Scheduler を pause した。8ジョブがすべて terminal、admission の active が空、使用済み回数が8であることを確認した。2つの生成 collection のみを非公開 snapshot に保存し、private GCS へ新規作成限定で退避、読み戻しの SHA256 を照合した。

新しい DB には8ジョブと2 control 文書を一度の atomic commit でコピーし、native fields の一致と source の変更がないことを再確認した。owner・preset snapshot・履歴・掲載 aggregate・使用済み回数を初期化していない。旧 DB と旧 collection は保持している。

manager の匿名 health は403、権限を持つ運用者の health は200、application 側の service 認証がない業務 API は401を確認した。公開 runtime の実 ID token を使う候補リビジョンで UI・API と過去の非 live 生成4件の詳細・交流情報を確認してから、公開 traffic を切り替えた。専用 Scheduler から新しい manager の同期 endpoint への OIDC 呼出しも200だった。

## 残る制約

- 公開 runtime の侵害では community DB へのアクセスと、owner を偽装した残枠内の正規生成操作が可能である。manager が守るのは固定 spec・同時1件・最大10 attempts・30分・期限であり、公開 runtime の RCE 自体を防ぐものではない。
- 公開媒体の大量取得による CDN/LB/egress 課金を、この edge policy だけで抑え切ることはできない。既存 Budget と停止 guard は通知遅延もあるため厳密な hard cap ではない。
- 現在の Google アカウントには課金アカウントの Budget 一覧権限がなく、既存 Budget の再読取はできなかった。既存 Budget は変更せず、承認済み USD300・停止期限・Cloud Run spend cap を維持した。
- PITR と日次 schedule の設定確認は、復元成功の実証と区別する。別 DB への restore と、実資源を削除する停止試験は行っていない。
- IAM の付与内容と実通信は確認したが、別 SA の権限を Policy Troubleshooter で判定する API は未有効である。`SERVICE_DISABLED` を対象 SA のアクセス拒否とは扱わない。全サービス・将来の workload・project 外の参照を網羅した無権限証明ではない。
- Storage / Firestore の大量読取に対する `DATA_READ` は有効にしていない。読取による持ち出しの全面的な監査は提供しない。
