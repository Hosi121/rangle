# バックエンドと配信の現状

2026-11-09時点。実装・接続確認と、規模・品質の成立確認を分ける。

## 実装済み

| 領域 | できていること | 残っていること |
| --- | --- | --- |
| フィード | 検索・タグ・limit/cursor・ETag。Flutterは20件ずつ継続取得し、重複排除・失敗ページの再試行・stale cursor復旧に対応 | 10万人規模の配信性能は未検証 |
| 版付き資産・配信先 | manifest検証、size/SHA照合、Range。GoのAPI/UIとGCS/CDNのmediaをHTTPSで接続し、モデルはCDNから直接取得 | 旧catalogへの復帰とcache混在の検証 |
| 生成・前処理 | 校正済み入力の共通前処理、静的3DGS・動的4DGS学習、scene-last保存 | 新素材の来歴・校正・品質の確認 |
| 生成結果 | 所有者による掲載・取り下げ、通常フィードへの統合。成功済み共有リンクは未掲載でもいいね・コメント可 | 品質判断と公開素材の追加 |
| 状態同期 | 個別GETでの更新に加え、有効時は専用OIDC identityのSchedulerが2分ごとにactive 1件を同期 | 期限・予算到達時の停止実行は別検証 |
| 品質比較 | サーバー固定の比較profile、held-out評価、保存形式・圧縮の比較 | 候補の採用判断と素材ごとの評価 |
| サーバー運用 | PORT・health・ヘッダーtimeout・終了待ち・slog、生成attemptと期限の制限 | 規模に応じた監視・過負荷制御・障害対策 |
| catalog | 起動時の不変snapshot検証、GCS readonly mount、生成metadataの永続registry | static catalog公開工程と複数revisionの復帰確認 |
| ABR | Goの基準実装と合成データによるテスト | 実Gaussian・クライアント再生との接続 |

発表用Cloud Runの設定はmin0/max1・concurrency40。費用を抑える設定であり、10万人向けの容量根拠ではない。詳細は [API仕様](api-delivery.md)、[フィード継続取得](feed-pagination.md)、[生成](generation.md)、[掲載と状態同期](generated-publication.md)、[stg配備](stg-deployment.md) を参照。共通前処理は[入力準備](dataset-preprocessing.md)、品質値と比較条件は[品質比較](training-quality.md)・[圧縮比較](temporal-compression-comparison.md)に記録する。

2026-11-09 01:57 UTC、revision `rangle-stg-00032-z5k` で匿名POSTの401とSchedulerの正式OIDCによる200を確認した。所有者の状態取得なしでqueued→succeededが反映され、Firestoreのactive解除・used=4不変も確認した。

## static catalog応答の事前生成

各clipのJSONとETagを起動時に生成し、詳細応答で再利用する。一覧は必要なclipのJSONだけを結合する。応答bytes・ETag・cursorの契約を保ち、ページの組合せごとに蓄積するキャッシュは持たない。保持するのはメタデータで、Gaussian本体は含まない。

Go 1.27.3、固定CPUコア・GOMAXPROCS=1、合成100clip、各200ms×5回、変更前後の順序を交替した中央値。比較元は `0df2273` に同じbenchmarkを追加したもの。

| 要求 | 処理時間 µs/op（前→後） | メモリ割当回数/要求（前→後） |
| --- | --- | --- |
| 一覧10件 | 7.340 → 3.239 | 28 → 26 |
| 一覧50件 | 28.363 → 11.240 | 29 → 27 |
| 詳細1件 | 2.191 → 1.140 | 25 → 19 |

これはソケット・TLS・CDN・Firestore registryの読み取りを含まないhandlerの計測で、配信p99や同時視聴数を示さない。起動時のJSON生成・保持と引き換えに、要求時の処理を減らす。HTTP契約のテスト、`go test -race ./...`、`go vet ./...` で検証する。

## 残る検証

10万人CCUのSLO・端末条件・段階負荷は未検証で、ABRも実モデル再生には未接続。[#20](https://github.com/Hosi121/rangle/issues/20)、[#35](https://github.com/Hosi121/rangle/issues/35)で判断する。今回は生成・品質確認を優先し、監視・過負荷制御・障害対策の拡張は後続に回す。これらは大規模公開前の課題として残る。[#24](https://github.com/Hosi121/rangle/issues/24)、[#33](https://github.com/Hosi121/rangle/issues/33)

先行の1万要求テストは同一PC・HTTP/1.1 loopbackでの有限GET。APIは9,466/10,000件成功し、534件は生成側の一時ポート不足だった。TLS/CDNや持続視聴を含まず、数千〜10万人の配信性能は未検証。
