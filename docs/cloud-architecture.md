# rangle のクラウド構成

発表用stgを、全体・ML・配信の3図で示す。2026-11-10時点の `origin/stg`（`10bd7cd`）のcode・IaCと既存の検証記録に基づき、クラウドの現在状態を再照会したものではない。正本は `infra/gcp/environments/presentation` → [presentation module](../infra/gcp/modules/presentation/) と [配備手順](stg-deployment.md)。汎用stgのplatform moduleとは別の構成である。

| 図 | 読み取ること | 実装の入口 |
| --- | --- | --- |
| [全体構成を開く](diagrams/overview.html) | Flutter Web、Go API、Google Cloud上の有限GPU生成、CDN、下段のCI/CD | [Goの接続](../cmd/rangle-server/application.go)、[LBとCDN](../infra/gcp/modules/presentation/edge.tf) |
| [ML処理を開く](diagrams/ml-pipeline.html) | 管理者の受付と共通台帳、NPZ/bundle、offlineとliveの分岐、検証・公開の出口 | [CustomJob](../internal/generation/vertex.go)、[offline worker](../ml/rangle_training/train.py)、[live worker](../ml/rangle_live/cloud.py) |
| [配信処理を開く](diagrams/delivery.html) | feed/manifest・LIVE indexと、不変資産の取得、Worker復号・WebGL描画 | [LIVE配信契約](live-streaming.md)、[JS viewer](../apps/web/viewer/src/session.js)、[Worker](../apps/web/viewer/src/temporal-worker.js) |

各HTMLはアイコン・日本語フォントを内包する。青実線はHTTP・モデルデータ、灰実線は制御・メタデータ、破線はCI/CD。全体図のHTTP双方向矢印は要求と応答を示す。PNGはこのHTMLからlight/darkの両方を生成する。[再生成・出典](diagrams/README.md)

図は実装・設定による接続を示し、すべての機能が無条件で有効になるという意味ではない。生成は不変configのSHA、LIVEは専用設定とfeature flagを使って有効にする。[生成基盤](generation-infrastructure.md)と[クラウドLIVEの配備条件](cloud-live.md#権限と配備)を参照。

- GPUはVertex AI CustomJobのTokyo Spot T4×1。Cloud RunはCPUのUI/APIであり、常駐GPUや独立したGCE workerを採用した図ではない。
- 入力は承認済みの校正・同期済み複数カメラ録画とseed。任意の単眼uploadやRTMP/RTSP受信は含まない。liveでは録画を元のPTSで供給し、到着した画像だけで更新する。提供seed・校正は事前情報を含む。
- クライアントはFlutter **Web**とJS iframe viewer。native mobileの実装・端末性能や大規模同時視聴を示すものではない。ABR・差分codecをこの配信経路へ追加した図でもない。
- 実GPUで入力→逐次生成→CDN公開は確認済みだが、**30秒の遅延を継続して満たす目標は未達**。起動待ち・画質・破棄window・測定範囲は[実測](cloud-live.md#2026-11-09の実gpu検証)を参照する。

LIVEの処理待ちは最新window 1件で、評価4視点の画素は学習から除外する。配信側も待ち更新を最新1件にまとめ、新モデルの準備中は旧モデルと視点を保持する。通常のpreview動画はFlutter側の別経路で再生し、Goは大容量モデルを中継しない。

## 旧配備図（2026-11-08）

以下は初期の配信・配備図として保持する。ML生成とLIVEの更新経路を含まないため、現在の全体説明には上の3図を使う。

![rangle: Cloudflare DNSからGoogle HTTPS LBへ接続し、Cloud RunのUI/APIとCloud CDN・private GCSのmediaへ分岐する構成。下段はWIFによる配備と予算・期限停止。](diagrams/cloud-architecture.svg)

[SVGを開く](diagrams/cloud-architecture.svg) · [発表用PNGを開く](diagrams/cloud-architecture.png)

## 配信経路

- **アプリ**：ブラウザ → `app.rangleplay.stream` → global external HTTPS Application Load Balancer → Tokyo の serverless NEG → Cloud Run `rangle-stg`。Go が `/` の landing、`/demo/` の Flutter Web、`/api/v1/clips` の API を配信する。app backend の CDN は無効。
- **メディア**：同じ LB の `media.rangleplay.stream` → Cloud CDN → private GCS media。公開許諾済みの不変 bytes を CDN からブラウザへ直接配信し、大容量 media を Go に中継させない。TTL は24時間。media bucket は PAP enforced / `allUsers` なしで、CDN の cache-fill service account に objectViewer を付与する。
- **カタログ**：Cloud Run の runtime SA が専用 private GCS catalog を read-only FUSE mount し、`/mnt/catalog/snapshots/<sha256>.json` を読む。入力・重み用の inputs と Terraform state は公開 media と別の private bucket に保持する。
- **DNS**：Cloudflare の app / media A record は Google LB の global IPv4 を返す **DNS only** 設定。ブラウザの HTTP 通信は Google の LB へ直接接続する。[Cloudflare proxy status](https://developers.cloudflare.com/dns/proxy-status/)

Cloud Run は `asia-northeast1`、1 vCPU / 512 MiB、request billing、min 0 / max 1、concurrency 40。発表用の小さい設定で、10万人同時視聴の容量実証は行っていない。既定 `run.app` と candidate-tag URL は CI の smoke 用に公開のまま維持する。

## 配備と停止

GitHub Actions の `stg` workflow は OIDC / Workload Identity Federation を経て、専用 deployer SA を使用する。長期 SA key を使わず Artifact Registry へ image を格納し、Cloud Run の candidate revision を smoke してから traffic を昇格する。

| 所有者 | 管理するもの |
| --- | --- |
| Terraform | infrastructure、IAM、catalog SHA、mount、runtime args、scaling |
| CI | container image と revision traffic |

承認済み総予算は **USD 300**。専用の JPY 30,000 budget が Pub/Sub へ通知し、Cloud Scheduler が15分ごとに Workflows を起動する。Workflow は通知の額と期限を確認し、**JPY 15,000 または 2026-11-14 15:00 UTC（11月15日00:00 JST）**で専用 deployer SA を無効化し、owned Cloud Run・LB forwarding rules を削除、専用 global IP を解放する。成功後に private inputs へ receipt を残し、Scheduler を pause する。private data / state / Artifact Registry は保持する。

請求通知の遅延・確認間隔・保存費用があるため、これは総額の厳密な hard cap ではない。既存の Cloud Run 200円 spend cap も維持し、先に作動する可能性がある。停止の詳細と実 IAM の制約は [stg 配備手順](stg-deployment.md#予算期限の停止) を参照。

## 検証の到達点

Cloud Run / NEG / LB / Cloud CDN / private GCS は作成済み。app / mediaの独自ドメインでHTTPS接続、UI・API、mediaのRange・CORS・ETag・実ファイルのハッシュ一致と、GitHub WIF経由の配備を確認済み。図は配信・配備の構成を表し、配備状況の更新先は [配備手順](stg-deployment.md#検証と到達点)。

guard は期限前の `stopped=false / reason=not_due`、Scheduler による起動、費用0円の検証通知の decode・閾値判定・ack を確認済み。予算・期限到達時に資源を削除する分岐と、その実 IAM は未検証。実際の到達点の更新先は [stg 配備手順](stg-deployment.md#検証と到達点)。

## 旧配備図の再生成

SVG は1920×1080、PNG は3840×2160。文字・線・図形・公式アイコンのパスを含む SVG で、スクリーンショットではない。生成元にアイコンを埋め込んでいるためネットワーク接続は不要。

```bash
python3 docs/diagrams/cloud-architecture.py
# PNGも更新する場合（CairoSVG 2.9.1が必要）
python3 docs/diagrams/cloud-architecture.py --png
```

Google の [公式アイコン](https://cloud.google.com/icons) と [製品アイコンガイド（2026年5月版）](https://services.google.com/fh/files/misc/google-cloud-product-icons.pdf) を使用。アイコン・商標は Google LLC に帰属する。新しい3図の再生成は[別手順](diagrams/README.md)を使う。

## GCP向けスキルの確認

Google 公式の [google-cloud-solution-architecture](https://github.com/google/skills/tree/e2071e8c958792b9d03d5839fe885b0171ffeaee/skills/cloud/google-cloud-solution-architecture) を調査した。新規構成の要件整理・設計・検証を扱うスキルで、今回は既存構成の図示のためインストールせず、公式の製品資料とアイコンを参照した。

## 参照した一次資料

- [Global external ALB と serverless NEG](https://docs.cloud.google.com/load-balancing/docs/https/setup-global-ext-https-serverless)
- [Cloud CDN の backend bucket / private origin](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket#make_your_cloud_storage_bucket_private)
- [Cloud Run の Cloud Storage mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)
- [Deployment pipelines の WIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)
- [Cloud Billing の Pub/Sub 通知](https://docs.cloud.google.com/billing/docs/how-to/budgets-programmatic-notifications)
