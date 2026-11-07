# インフラ構成案

設計段階の提案。ピーク時に数千〜数万人が同時視聴する規模を目標とする。クラウド資源の作成・起動と、この規模の負荷検証は未実施である。

## 推奨する構成

Go APIとFlutter Webの静的成果物を1つのHTTPコンテナにまとめ、Cloud Runを実行先の候補とする。本番の大きな版付き資産はCDNとCloud Storageから取得し、Python/CUDAの再構成は有限のCustomJobへ分ける。

配信（data plane）、Go APIの制御（control plane）、GPU生成（generation plane）を分離する。生成済み資産を共有し端末で描画するため、同時視聴者数に合わせて専用サーバーGPUを増やす構成にしない。

```mermaid
flowchart LR
    Browser[Flutter Web] --> App[Cloud Run: Go API + Web]
    Browser --> CDN[本番: CDN]
    CDN --> Assets[Storage: 配信用資産]
    App --> Assets
    Input[Storage: 非公開入力・重み] --> GPU[CustomJob: Python/CUDA]
    GPU --> Input
    GPU -->|評価後に公開| Assets
```

ローカル/MVPではアプリを1サービスに抑え、Storage直接取得で成立を確認してよい。本番のGo APIは単一インスタンスへ状態を閉じず水平分散し、資産の全バイトを中継しない。UIの独立したリリースが必要になったら配信先を分ける。現在の `rangle-server` はWeb・フィードAPI・版付き資産をローカルで提供し、取得先originの設定で大容量配信を分離できる。クラウド用の待受けとコンテナ化・実配備は後続とする。[APIと配信の契約](api-delivery.md)

## 可搬性の境界

| 共通に保つもの | 実行環境に閉じるもの |
| --- | --- |
| Go/WebのOCIコンテナ、HTTP、ヘルスチェック | Cloud Run、IAM、スケール設定 |
| 資産の論理path、版、manifest、チェックサム | bucket、配信origin、CORS、認証 |
| Python/CUDAコンテナ、CLI引数、入力・出力の形式 | ジョブ投入・状態照会、GPUの種類、Storageへの転送 |

ABRや描画処理へクラウドSDKを広げず、実行先・取得元の接続部分を分ける。Cloud Runの `PORT` と待受け条件に合わせ、永続データはStorageに保存する。CUDAコンテナのGPU・driver適合は実機で確認する。[Cloud Runの実行契約](https://docs.cloud.google.com/run/docs/container-contract)、[CustomJob](https://docs.cloud.google.com/vertex-ai/docs/training/create-custom-job)

CDNを省く判断はローカル/MVPに限定し、数万同時視聴を目指す本番配信にはCDNとorigin保護を含める。Cloud CDNは外部Application Load Balancerとbackend bucketを伴うため、これらはprovider固有の設定としてIaCに閉じる。常駐GPU、GKE、ワークフロー基盤は現時点の必須構成にしない。[Cloud CDNの構成](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket)

origin保護では、immutable revisionを含むcache keyとCache-Control、同じkeyへのcache fill集約、originへの直接アクセスの公開範囲を設計する。キャッシュが冷たい状態や人気の集中でも、origin帯域・RPSが許容範囲かを確認する。CDNが常にhitすると仮定しない。[Cloud CDNのキャッシュとrequest collapsing](https://docs.cloud.google.com/cdn/docs/caching)

APIのconcurrency・最大インスタンス数、依存先の容量、配信とGPUのquota・region・費用上限は別途確定し、[段階的な容量検証](technology-selection.md#同時視聴の容量検証計画)で達成を判断する。[Cloud Runのautoscaling](https://docs.cloud.google.com/run/docs/about-instance-autoscaling)

## IaCの分割案

HCLとGoogle providerを候補とし、実装前にTerraformかOpenTofuの一方へ固定する。アプリの可搬性と、Google固有resourceを別クラウドへ移植する作業は区別する。

- `stg`・`main`は別root・別state・別実行identityにする。CLI workspaceの切替だけで隔離しない。
- stateは非公開の専用Storageに置き、lockとObject Versioningを使う。state用bucketの初期作成はbootstrapとして分ける。
- CLI・providerの版を固定し、lock fileを管理する。最初から汎用クラウドmoduleを作らない。
- CI認証はOIDC/WIFを候補とし、repositoryとrefを制限する。GPUジョブの実行は明示的な別操作にし、IaC適用で学習を起動しない。

根拠: [Googleのroot-module指針](https://docs.cloud.google.com/docs/terraform/best-practices/root-modules)、[Terraform GCS backend](https://developer.hashicorp.com/terraform/language/backend/gcs)、[OpenTofu GCS backend](https://opentofu.org/docs/language/settings/backends/gcs/)、[CI向けWIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)。

## 実装前に決めること

project、region、quota、公開する資産、費用・GPU時間・ジョブ並列数の上限、stateと実行権限の管理者を決める。容量検証の実行予算と中止条件も別途定め、現在は起動可否、配信性能、GPU互換性、費用を未検証として扱う。
