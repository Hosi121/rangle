# rangle ドキュメント

[製品概要・最初の起動](../README.md) · [公開アプリ](https://app.rangleplay.stream/) · [開発ルール](../CONTRIBUTING.md)

現状は[バックエンド](backend-status.md)、全体像は[クラウド構成](cloud-architecture.md)から確認できます。

## 起動手順

| 資料 | 用途 |
| --- | --- |
| [Flutter Web](../apps/web/README.md)・[Docker](docker.md) | 開発・ビルド・APIモード |
| [ローカルscene配信](local-server.md)・[私有model preview](private-preview.md) | 手元の資産確認、管理者限定preview |

## 構成・再生

| 資料 | 用途 |
| --- | --- |
| [クラウド構成](cloud-architecture.md) | [全体](diagrams/overview.html)・[ML](diagrams/ml-pipeline.html)・[配信](diagrams/delivery.html)・[監視](diagrams/observability.html)の4図 |
| [連続再生](temporal-playback.md)・[クライアント処理](client-processing.md) | RGS1、Worker、GPU描画・資源管理 |
| [没入UI](immersive-ui.md) | 動画/3D切替、時刻・視点・再生意図 |
| [LIVE配信](live-streaming.md) | index、model交換、再接続 |

## API・配信

| 資料 | 用途 |
| --- | --- |
| [APIと版付き資産](api-delivery.md) | catalog、manifest、Range/ETag/cache |
| [ページング](feed-pagination.md)・[過負荷](api-overload.md) | cursorと要求制限 |
| [アカウント/交流](accounts.md)・[保存先](community-storage.md) | 認証・いいね・コメント・永続化 |
| [生成API](generation.md)・[結果掲載](generated-publication.md)・[掲載UI](generation-publication-ui.md) | preset、job、公開操作 |
| [資産公開](asset-publication.md)・[不変catalog](catalog-snapshots.md) | 許諾・bytes検証、配置・切替・復帰 |
| [配信部品の契約](streaming-contract.md)・[HTTP選択](http-delivery-decision.md) | GoのHTTP/ABR基準実装 |
| [言語間fixture](../testdata/contracts/v1/README.md)・[gzip契約](rgs-gzip-contract.md) | schema、74入力、単一member・末尾余剰の採否 |

## ML・データ

| 資料 | 用途 |
| --- | --- |
| [前処理](dataset-preprocessing.md)・[MLコンテナ](../ml/container.md) | 校正画像・動画・点群、再現環境 |
| [動的4DGS](dynamic-gs-training.md)・[ライブ学習](live-training.md) | 共同学習と到着画像による更新 |
| [学習品質](training-quality.md)・[時間モデル圧縮](temporal-compression-comparison.md) | 現行profile・held-out・容量/誤差比較 |
| [可逆packing形式](gaussian-compression.md) | RGS1-Z。float→保存形式の量子化とは別 |
| [初期VRU比較](gaussian-quality.md)・[UVG-GS比較](gaussian-training.md) | 素材・条件の異なる過去実験 |

## インフラ・配備

| 資料 | 用途 |
| --- | --- |
| [stg配備](stg-deployment.md)・[Terraform](../infra/gcp/README.md) | 環境・IAM・予算/期限・停止 |
| [CI/CD](cicd.md)・[生成基盤](generation-infrastructure.md) | 両serviceの配備・復帰、有限GPU・config |
| [ログ方針](logging.md)・[監視](observability.md) | 出力属性・検索・ダッシュボード |
| [防御/復旧](security-hardening.md)・[identity整理](security-identity.md) | Armor・backup・監査・IAM |
| [台帳移行](security-generation-migration.md)・[調査記録](security-review.md) | データを保つ切替と検証の限界 |

## 研究・現状

| 資料 | 用途 |
| --- | --- |
| [バックエンドの現状](backend-status.md) | 実装・未接続・未検証 |
| [クラウドLIVE実測](cloud-live.md)・[API/CDN測定](live-delivery-measurements.md) | 生成遅延と配信測定を分けて確認 |
| [品質/圧縮の研究調査](research/gs-quality-compression-2026.md) | 一次資料・license・比較案。未採用の提案を含む |
| [企画](proposal.md)・[選定](technology-selection.md)・[初期infra](infrastructure-design.md)・[ABR調査](streaming-research.md) | 初期の仮説・候補・判断理由 |
| [提出案内](submission.md) | 開発用と最終提出先の違い |

数値は各資料のデータ・評価条件と合わせて読み、初期案を現在の採用構成と混同しないでください。出典・利用条件は各素材の資料と[ATTRIBUTION](../apps/web/assets/samples/ATTRIBUTION.md)を参照。
