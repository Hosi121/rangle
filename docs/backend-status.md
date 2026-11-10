# バックエンドと配信の現状

2026-11-10のコードと既存検証記録の概要。実装済みと、性能・規模の実証を区別する。

## 実装済み

| 領域 | 到達点・正本 |
| --- | --- |
| feed/catalog | 検索・タグ・20件継続取得、ETag、stale cursor復旧、不変snapshotと掲載registry。[ページング](feed-pagination.md)・[catalog](catalog-snapshots.md) |
| 配信 | HTTPSのGo UI/APIとprivate GCS/CDN、Range・SHA照合、model直接取得。[API](api-delivery.md)・[公開preflight](asset-publication.md) |
| 生成 | 共通前処理、静的3DGS・動的4DGS、scene-last保存、有限台帳、団体所有の生成・取消・公開。[生成](generation.md)・[担当者API](publisher-auth.md)・[品質](training-quality.md) |
| 掲載・交流 | owner掲載/取り下げ、声援・本人履歴、未掲載の成功済み共有リンクでも交流可。[掲載](generated-publication.md)・[アカウント](accounts.md) |
| 団体ページ | 公開プロフィール・公開動画一覧、一般ユーザーのフォローと本人の一覧。担当者の所属・権限とは別に保存。[アカウント](accounts.md#団体ページとフォロー) |
| 同期・権限 | IAM必須managerと別DB、専用OIDC同期、最大10 attempts/同時1件/期限。[生成基盤](generation-infrastructure.md)・[適用記録](security-review.md) |
| 運用・release | 8要求の処理枠、timeout/slog、WAF・読取制限、監査・backup、両serviceの候補検証/復帰/receipt。[過負荷](api-overload.md)・[CI/CD](cicd.md)・[監視](observability.md) |
| ログ監視 | Tokyoの専用bucket/sink・30日保持・8 widgetsのdashboard。Go再配備後のseverity/message受信を確認し、[#33](https://github.com/Hosi121/rangle/issues/33)は完了。[受信記録](observability.md#go再配備後の受信確認) |
| 再生・圧縮 | 時間モデルの端末描画、local-lifetime学習、可逆packing。FP16/間引きは比較用。[再生](temporal-playback.md)・[圧縮比較](temporal-compression-comparison.md) |
| LIVE | Tokyo Spot T4×1で8モデルを実CDN公開しterminal・active解放を確認。継続30秒遅延は未達。[実測](cloud-live.md) |
| ABR | Go基準と照合したJS制御、API/manifestからsize/SHA検証・RGS1描画まで接続。独立区間の公開gateも実装。実品質ラダー・実端末/QoEの比較は未検証。[契約](streaming-contract.md) |
| 私有model確認 | 管理者限定のローカルpreview。公開catalogには登録しない。[手順](private-preview.md) |

公開Runのruntime設定と適用段階は[配備手順](stg-deployment.md)と[#24](https://github.com/Hosi121/rangle/issues/24)で管理する。配備・小規模疎通の成功を、10万CCUや実端末の容量根拠とはしない。素材ごとの来歴・校正・画質判断は継続する。

## static catalog応答の事前生成

各clipのJSON/ETagを起動時生成し、一覧は必要分だけ結合する。応答bytes・ETag・cursorを維持し、ページ組合せcacheは作らない。保持対象はmetadataのみ。

Go1.27.3、固定CPU・GOMAXPROCS=1、合成100clip、200ms×5回を前後交替した中央値。比較元は`0df2273`に同じbenchmarkを追加したもの。

| 要求 | 処理時間 µs/op（前→後） | メモリ割当回数/要求（前→後） |
| --- | --- | --- |
| 一覧10件 | 7.340 → 3.239 | 28 → 26 |
| 一覧50件 | 28.363 → 11.240 | 29 → 27 |
| 詳細1件 | 2.191 → 1.140 | 25 → 19 |

ソケット/TLS/CDN/Firestoreを含まないhandler測定。起動時の生成・保持と引き換えの改善で、配信p99・同時視聴数は示さない。HTTP契約テスト・Go race/vetで検証した。

## UI圧縮と端末でのモデル再利用

Web release buildのJS/Wasm等をgzipで配信し、公開VODの検証済みencoded modelは固定2枠・各16MiB以下でviewerをまたいで再利用する。cache hitはABR帯域sampleへ混ぜず、失効・改ざん・容量不足は通常取得へ戻す。[配備](stg-deployment.md#webアプリ本体の圧縮配信)・[取得とcache](transport-playback.md#取得交換取消)を参照。

#125の完了はCDNのHTTP/CORS/cache-key契約の確認で、UI圧縮やA→B→Aの再訪を含んでいなかった。後続のPR137ではABRの実転送計測に`no-store`を採用したため、この再訪cacheを別途実装した。モデルの実品質ラダー、端末全体のCPU/GPU peak、CDN人気分布・hit率/origin負荷は引き続き未検証。

## 残る検証

- 10万CCUのSLO・実端末・段階負荷・cold origin集中、実負荷下の版混在/復帰は未検証。[#20](https://github.com/Hosi121/rangle/issues/20)・[#35](https://github.com/Hosi121/rangle/issues/35)
- LIVEは録画ZIP入力のクラウド公開まで実装済み。RTMP/RTSP入力は未実装、長時間・実カメラ・欠損なし25fpsは未検証、継続30秒遅延は未達。FP16/間引きは未採用、実品質ラダーを使うABR比較と実端末測定は残る。[#89](https://github.com/Hosi121/rangle/issues/89)・[#27](https://github.com/Hosi121/rangle/issues/27)・[#28](https://github.com/Hosi121/rangle/issues/28)
- 分散quota・容量設定と、SLO/費用モデル・段階リリースの受入は[#24](https://github.com/Hosi121/rangle/issues/24)・[#20](https://github.com/Hosi121/rangle/issues/20)・[#34](https://github.com/Hosi121/rangle/issues/34)に残る。SLO通知・端末QoEは完了した#33の対象外。
- 旧[#32](https://github.com/Hosi121/rangle/issues/32)の残条件を分割し、公開app originのWeb/Worker・Range/条件付きGET・cache/公開切替は[#125](https://github.com/Hosi121/rangle/issues/125)で確認完了。有限のcold/warm・CDN hit率/origin観測は[#126](https://github.com/Hosi121/rangle/issues/126)に残る。
- gzip追加memberの採否差は[#115](https://github.com/Hosi121/rangle/pull/115)で修正をstgへマージ済み。通常資産の可逆性、列挙fixtureと確認browserの範囲、追加復号負荷は[gzip契約](rgs-gzip-contract.md)を参照。FP16採用とは別の変更。
- 先行の1万有限GETは同一PCのHTTP/1.1 loopbackで9,466成功、534件はclient側の一時port不足。TLS/CDN・持続視聴の性能根拠にはしない。
