# rangle のクラウド構成

発表用presentation環境の構成。正本は[presentation module](../infra/gcp/modules/presentation/)と[配備手順](stg-deployment.md)。2026-11-10の[適用記録](security-review.md)を反映している。

| 図 | 内容・実装 |
| --- | --- |
| [全体](diagrams/overview.html) | Flutter Web、Go、Vertex AI、CDN、CI/CD、横断するログ監視 |
| [ML処理](diagrams/ml-pipeline.html) | 認証→生成manager→専用台帳→[CustomJob](../internal/generation/vertex.go)→検証・公開 |
| [配信処理](diagrams/delivery.html) | feed/manifest・LIVE index→不変資産→[Worker復号](../apps/web/viewer/src/temporal-worker.js)→WebGL |
| [ログ監視](diagrams/observability.html) | 4種の収集元→専用Logging bucket/view、標準メトリクス→Monitoring |

HTMLはアイコン・フォント内包でオフライン閲覧できる。データ/制御/CI/観測の線種は各図の凡例を参照。[PNG出力・再生成・出典](diagrams/README.md)

## 配信経路

| 経路 | 構成 |
| --- | --- |
| アプリ | `app.rangleplay.stream` → HTTPS LB → serverless NEG → Cloud Run `rangle-stg`。`/` landing、`/demo/` Flutter、`/api/` Go。CDN無効 |
| メディア | `media.rangleplay.stream` → 同じLB → Cloud CDN → private GCS。モデルは不変URL・TTL24時間、LIVE indexはTTL1秒 |
| カタログ | private GCSの`snapshots/<sha256>.json`をGoへread-only FUSE mount。catalogだけversioning無効 |
| DNS | Cloudflareのapp/media A recordはGoogle LBへ向けた[DNS only](https://developers.cloudflare.com/dns/proxy-status/) |

公開RunはTokyo、1 vCPU/512MiB、request billing、min0/max1、concurrency40。端末が復号・描画し、Goは大容量モデルを中継しない。生成・LIVEは対応する不変configとfeature flagで有効にする。

## ネットワーク境界とアクセス制御

**VPC・subnetは未使用**。CustomJobのNetwork、RunのVPC connector/Direct VPC interfacesも未設定。図のCloud枠はサービスの所属を表す。

| 対象 | 境界 |
| --- | --- |
| 公開VIP | `34.96.101.8/32`（2026-11-09確認）。単一の公開IPで、subnet CIDRではない |
| 公開Run | `internal-and-cloud-load-balancing` ingress。外部入口はLBのみ。invoker IAMチェックは無効、操作認可はGo。既定run.app・候補tagへの外部直通は404 |
| 生成manager | ingress ALL＋Cloud Run IAM必須。公開LBには載せない。業務APIは署名付きservice identityも検証 |
| inputs/catalog | PAP enforced＋用途別IAM。匿名公開しない |
| media | private bucketをCDN cache-fill SAが読む。許諾済み素材のみ公開。Cloud Armorで公開prefixを制限 |
| Firestore/Vertex/AR | IAMで制御。private subnetによる隔離ではない |

通常・候補backendの両方にCloud Armorを付ける。`X-Rangle-Candidate: ci-candidate`は公開の経路選択子で、認証用の秘密ではない。[入口制限手順](stg-deployment.md#lb入口制限への切替)・[防御設定](security-hardening.md)

## 生成制御とデータの分離

同じimageを公開UI/APIとprivate生成managerの別mode・別SAで動かす。

| identity | 許可する操作 |
| --- | --- |
| `rangle-stg-runtime` | community DB `rangle-stg`、catalog読取、manager invoke。Vertex・生成DB・trainer actAsは不可 |
| `rangle-stg-generation` | 生成DB `rangle-stg-generation`、config/catalog読取、期限付きVertex操作・trainer actAs |
| 専用同期SA | managerのactive同期のみ。DB・Vertex権限なし |

公開側がadmin cookie・role・CSRFを検証し、署名付きservice tokenとownerをmanagerへ渡す。managerは固定preset・同時1件・最大10 attempts・30分・期限を守る。公開runtime侵害時に残る操作範囲は[セキュリティ記録](security-review.md#残る制約)、台帳切替は[移行手順](security-generation-migration.md)を参照。

GPUはTokyo Spot T4×1の有限CustomJob。入力は校正・同期済み複数カメラ録画とseedで、単眼upload・RTMP/RTSP受信は含まない。提供校正・seedの事前情報、破棄window、**継続30秒遅延の未達**は[クラウドLIVEの実測](cloud-live.md#2026-11-09の実gpu検証)に記載する。native mobile性能・大規模視聴・実GaussianのABR切替は未検証。

## ログ監視の経路と到達点

Run、Vertex worker、LB/CDN、Scheduler/Workflowsの4系統を専用sinkで選び、TokyoのLogging bucket `rangle-stg-logs`へ30日保持する。Cloud Storage bucketとは別資源で、監査ログは対象外。既存`_Default`/`_Required`は変更しない。

MonitoringのログパネルとLogs Explorerは同じ`_AllLogs` viewを読む。公開Runの要求数/秒・revision別p95は標準メトリクスで、ログ由来でもブラウザ描画時間でもない。manager有効時はそのログも収集する。[検索・検証範囲](observability.md)・[出力方針](logging.md)

## 配備と停止

TerraformがIAM・mount・args・catalog・scaling、CIがimage/trafficを管理する。GitHub WIFで認証し、managerの認証付きcandidate→公開アプリのLB candidateの順で確認・昇格する。後続失敗時は両serviceのtrafficを戻し、DB/IAMは戻さない。[CI/CD](cicd.md)

予算・期限でScheduler/Workflowsがowned資源を停止する。保持データ、IAMの制約、通知遅延による費用上限の限界は[停止手順](stg-deployment.md#予算期限の停止)に集約する。

## 検証の到達点

LB経由のUI/API、媒体のRange/CORS/ETag/hash、匿名origin拒否、WIF配備、manager分離、監査・ログ収集を確認済み。停止guardの通常実行は成功。破壊的な停止分岐・restore・端末性能・大規模視聴は未検証。[配備記録](security-review.md)

## 旧配備図（2026-11-08）

初期の配信・配備のみを表し、ML/LIVE・manager・ログ監視を含まない。[SVG](diagrams/cloud-architecture.svg)・[PNG](diagrams/cloud-architecture.png)

## 旧配備図の再生成

```bash
python3 docs/diagrams/cloud-architecture.py
# PNGも更新する場合（CairoSVG 2.9.1が必要）
python3 docs/diagrams/cloud-architecture.py --png
```

SVGは1920×1080、PNGは3840×2160。Googleの[公式アイコン](https://cloud.google.com/icons)を使用し、商標・アイコンはGoogle LLCに帰属する。新4図の手順は[図のREADME](diagrams/README.md)を参照。

## 参照した一次資料

[LB/serverless NEG](https://docs.cloud.google.com/load-balancing/docs/https/setup-global-ext-https-serverless)・[CDN/private origin](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket#make_your_cloud_storage_bucket_private)・[GCS mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)・[WIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)・[Budget通知](https://docs.cloud.google.com/billing/docs/how-to/budgets-programmatic-notifications)
