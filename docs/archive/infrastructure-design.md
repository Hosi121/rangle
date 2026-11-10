# 初期インフラ構成案

初回配備前の判断記録。現在は[クラウド構成](../cloud-architecture.md)・[stg配備](../stg-deployment.md)・[Terraform](../../infra/gcp/README.md)を参照。設計目標の10万CCUは未検証（[#20](https://github.com/Hosi121/rangle/issues/20)）。

## 推奨する構成

| 処理 | 初期案 |
| --- | --- |
| UI・制御 | Go APIとFlutter Webを1つのCloud Runコンテナへまとめる |
| 大容量配信 | 版付き資産をStorage/CDNから端末へ直接配信する |
| 生成 | Python/CUDAを有限のVertex AI CustomJobで実行する |
| 保存 | 公開media、非公開catalog・入力・stateを分ける |

端末で描画し、生成済み資産を共有する。CDNなしの直接取得はローカル/MVPに限定し、本番はorigin保護とcache fill集約を設計する。常駐GPU・GKEは必須としない。

## 可搬性の境界

| 共通契約 | 環境固有の設定 |
| --- | --- |
| Go/WebのOCIコンテナ・HTTP・health | Cloud Run・IAM・scaling |
| 資産path・版・manifest・checksum | bucket・origin・CORS・認証 |
| Python/CUDAのCLI・入出力 | GPU・driver・job投入・Storage転送 |

クラウドSDKは接続部分に閉じる。永続データは外部保存し、`PORT`・待受けは[Cloud Run実行契約](https://docs.cloud.google.com/run/docs/container-contract)に合わせる。GPU互換性は実機で確認する。

## IaCの構成

- Terraform 1.17.1 / Google provider 8.10.0とlock fileを固定する。
- stg/mainは別root・state・identity。stateはprivate Storageに置き、lock・Object Versioningを使う。bucket作成はbootstrapへ分ける。
- mediaをHTTPS LB/CDNへ接続し、private originはcache-fill SAで読む。catalogは内容SHA付きpathをread-only FUSE mountする。FUSEの制約からcatalogだけversioningを無効にする。
- 初回は`application=null`で基盤を作り、検証済みsnapshotの配置後にアプリを追加する。IaCで素材をuploadしない。
- CI認証はrepository/ownerの数値IDとrefを制限したOIDC/WIFを候補とする。

この案の時点ではローカルコンテナのみ確認済みで、実配備・WIF作成・GPU実行は未実施だった。

根拠：[root module](https://docs.cloud.google.com/docs/terraform/best-practices/root-modules)・[GCS backend](https://developer.hashicorp.com/terraform/language/backend/gcs)・[CustomJob](https://docs.cloud.google.com/vertex-ai/docs/training/create-custom-job)・[private origin](https://docs.cloud.google.com/cdn/docs/configure-private-bucket-access)・[GCS mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)・[FUSE制約](https://docs.cloud.google.com/storage/docs/cloud-storage-fuse/overview#limitations)・[WIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)。

## 実配備前に決めること

project・region・quota、公開素材、GPU時間・費用・並列上限、DNS/state/IAM管理者を確定する。容量試験は[段階負荷計画](technology-selection.md#同時視聴の容量検証計画)に従い、cold cache・人気集中・origin帯域/RPS・API concurrencyを測る。CDN導入や[autoscaling](https://docs.cloud.google.com/run/docs/about-instance-autoscaling)の設定だけを容量の実証としない。
