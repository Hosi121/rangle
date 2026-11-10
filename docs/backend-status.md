# バックエンドと配信の現状

2026-11-10のコードと既存検証記録の概要。実装済みと、性能・規模の実証を区別する。

## 実装済み

| 領域 | 到達点・正本 |
| --- | --- |
| feed/catalog | 検索・タグ・20件継続取得、ETag、stale cursor復旧、不変snapshotと掲載registry。[ページング](feed-pagination.md)・[catalog](catalog-snapshots.md) |
| 配信 | HTTPSのGo UI/APIとprivate GCS/CDN、Range・SHA照合、model直接取得。[API](api-delivery.md)・[公開preflight](asset-publication.md) |
| 生成 | 共通前処理、静的3DGS・動的4DGS、scene-last保存、有限台帳。[生成](generation.md)・[品質](training-quality.md) |
| 掲載・交流 | owner掲載/取り下げ、未掲載の成功済み共有リンクでも交流可。[掲載](generated-publication.md)・[アカウント](accounts.md) |
| 同期・権限 | IAM必須managerと別DB、専用OIDC同期、最大10 attempts/同時1件/期限。[生成基盤](generation-infrastructure.md)・[適用記録](security-review.md) |
| 運用・release | 8要求の処理枠、timeout/slog、WAF・読取制限、監査・backup、両serviceの候補検証/復帰/receipt。[過負荷](api-overload.md)・[CI/CD](cicd.md)・[監視](observability.md) |
| 再生・圧縮 | 時間モデルの端末描画、local-lifetime学習、可逆packing。FP16/間引きは比較用。[再生](temporal-playback.md)・[圧縮比較](temporal-compression-comparison.md) |
| LIVE | Tokyo Spot T4×1で8モデルを実CDN公開しterminal・active解放を確認。継続30秒遅延は未達。[実測](cloud-live.md) |
| ABR | Go基準実装と合成テスト、実RGS1のsize/SHA/codec/working set広告。実モデルの複数segment・品質切替には未接続。[契約](streaming-contract.md) |
| 私有model確認 | 管理者限定のローカルpreview。公開catalogには登録しない。[手順](private-preview.md) |

発表用の公開Runはmin0/max1・concurrency40。配備・小規模疎通の成功を、10万CCUや実端末の容量根拠とはしない。素材ごとの来歴・校正・画質判断は継続する。

## static catalog応答の事前生成

各clipのJSON/ETagを起動時生成し、一覧は必要分だけ結合する。応答bytes・ETag・cursorを維持し、ページ組合せcacheは作らない。保持対象はmetadataのみ。

Go1.27.3、固定CPU・GOMAXPROCS=1、合成100clip、200ms×5回を前後交替した中央値。比較元は`0df2273`に同じbenchmarkを追加したもの。

| 要求 | 処理時間 µs/op（前→後） | メモリ割当回数/要求（前→後） |
| --- | --- | --- |
| 一覧10件 | 7.340 → 3.239 | 28 → 26 |
| 一覧50件 | 28.363 → 11.240 | 29 → 27 |
| 詳細1件 | 2.191 → 1.140 | 25 → 19 |

ソケット/TLS/CDN/Firestoreを含まないhandler測定。起動時の生成・保持と引き換えの改善で、配信p99・同時視聴数は示さない。HTTP契約テスト・Go race/vetで検証した。

## 残る検証

- 10万CCUのSLO・実端末・段階負荷・cold origin集中、実負荷下の版混在/復帰は未検証。[#20](https://github.com/Hosi121/rangle/issues/20)・[#35](https://github.com/Hosi121/rangle/issues/35)
- LIVEの長時間・実カメラ・欠損なし25fps、FP16/間引き採用、実ABR接続は未決定。
- 大規模公開向けの分散quota・SLO/通知・障害対策を拡張する。[#24](https://github.com/Hosi121/rangle/issues/24)・[#33](https://github.com/Hosi121/rangle/issues/33)
- 先行の1万有限GETは同一PCのHTTP/1.1 loopbackで9,466成功、534件はclient側の一時port不足。TLS/CDN・持続視聴の性能根拠にはしない。
