# rangle ドキュメント

[公開アプリ](https://app.rangleplay.stream/demo/) · [製品概要・起動](../README.md) · [開発ルール](../CONTRIBUTING.md)

## まず読む

| 知りたいこと | 資料 |
| --- | --- |
| 誰に何を提供するか | [企画](proposal.md) |
| 何が動き、何が未検証か | [実装状況と残る課題](backend-status.md) |
| どのサービスが何を担うか | [クラウド構成と4図](cloud-architecture.md) |
| ユーザー・団体・試合の関係 | [ドメインモデル](sports-domain.md) |

## 開発・画面

| 資料 | 内容 |
| --- | --- |
| [Flutter Web](../apps/web/README.md)・[Docker](docker.md) | 起動・ビルド・API接続 |
| [没入UI](immersive-ui.md)・[クライアント処理](client-processing.md) | 動画と3Dの切替、時刻、入力、資源管理 |
| [ローカル配信](local-server.md)・[管理者向けモデル確認](private-preview.md) | 手元のscene、認可付きsnapshot preview |

## API・配信契約

| 資料 | 内容 |
| --- | --- |
| [HTTP/API](api-delivery.md)・[ページング](feed-pagination.md)・[過負荷](api-overload.md) | 検索、catalog、Range/ETag/cache、要求制限 |
| [アカウントと交流](accounts.md)・[保存先](community-storage.md) | 声援履歴、コメント、団体ページ・フォロー |
| [団体担当者](publisher-auth.md)・[生成API](generation.md) | 団体RBAC、入力割当、生成、進捗、削除 |
| [結果掲載](generated-publication.md)・[掲載UI](generation-publication-ui.md) | 所有者確認、成果物検証、公開・取り下げ |
| [資産公開](asset-publication.md)・[catalog切替](catalog-snapshots.md)・[独立区間](segmented-publication.md) | 許諾、版、size/SHA、切替・復帰 |
| [配信部品](streaming-contract.md)・[transport再生](transport-playback.md) | Go/JSのABR、品質選択、区間取得、端末cache |
| [時間モデル再生](temporal-playback.md)・[LIVE配信](live-streaming.md) | RGS1、時刻更新、model交換、再接続 |
| [共有fixture](../testdata/contracts/v1/README.md)・[gzip契約](rgs-gzip-contract.md) | Go/Python/JS/Dartの採否・意味制約 |

## ML・測定

| 資料 | 内容 |
| --- | --- |
| [前処理](dataset-preprocessing.md)・[MLコンテナ](../ml/container.md) | 校正・同期済み画像/動画、点群、再現環境 |
| [動的4DGS](dynamic-gs-training.md)・[ライブ学習](live-training.md) | 学習表現と到着画像による更新 |
| [学習品質](training-quality.md)・[圧縮比較](temporal-compression-comparison.md)・[保存形式](gaussian-compression.md) | 現行profile、画質・容量、可逆packing |
| [クラウドLIVE](cloud-live.md)・[API/CDN測定](live-delivery-measurements.md) | 生成遅延と配信測定の条件・限界 |
| [CDNブラウザ契約](cdn-browser-verification.md) | 公開originでのCORS/Range/cache-key確認 |
| [品質・圧縮の研究調査](research/gs-quality-compression-2026.md) | 一次資料・利用条件・未採用の比較案 |

## インフラ・運用

| 資料 | 内容 |
| --- | --- |
| [stg配備](stg-deployment.md)・[Terraform](../infra/gcp/README.md) | 環境、IAM、予算・期限、停止 |
| [CI/CD](cicd.md)・[生成基盤](generation-infrastructure.md) | 両serviceの配備・復帰、有限GPUジョブ |
| [ログ](logging.md)・[監視](observability.md) | 構造化ログ、検索、進捗view、dashboard |
| [防御・復旧](security-hardening.md)・[identity](security-identity.md) | Armor、監査、backup、IAM |
| [台帳移行](security-generation-migration.md)・[適用記録](security-review.md) | データを保つ切替、確認済み範囲 |
| [図の再生成](diagrams/README.md)・[提出](submission.md) | 図の出典と出力、最終提出先 |

## 過去の判断記録

[archive](archive/README.md)に初期の技術・配信・インフラ案と過去の再構成実験をまとめています。現行の手順や採用構成は上記の資料を参照してください。数値は素材・評価条件と合わせて読み、出典・利用条件は各資料と[サンプルの出典](../apps/web/assets/samples/ATTRIBUTION.md)を参照してください。
