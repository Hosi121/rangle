# rangle のクラウド構成

発表用presentation環境の現行構成。2026-11-10 **09:14–09:19 UTC**にCloud Run・LB/CDN・manager IAM・DB・ログ・定期処理をread-onlyで照合し、`stg`の実装と合わせて4図を更新した。正本は[presentation module](../infra/gcp/modules/presentation/)と[配備手順](stg-deployment.md)。図は常駐GPU・実機性能・大規模配信の達成を表さない。

| 図 | 内容・実装 |
| --- | --- |
| [全体](diagrams/overview.html) | Flutter Web、Go、Vertex AI、CDN、CI/CD、横断するログ監視 |
| [ML処理](diagrams/ml-pipeline.html) | 運営/団体の認証→private manager→専用台帳→[CustomJob](../internal/generation/vertex.go)→掲載/削除 |
| [配信処理](diagrams/delivery.html) | feed/manifest URL・LIVE index→CDN→JS取得/ABR・端末cache→[Worker復号](../apps/web/viewer/src/temporal-worker.js)→WebGL |
| [ログ監視](diagrams/observability.html) | 4種の収集元→専用Logging bucket、標準指標→Monitoring、進捗専用view→manager |

HTMLはアイコン・フォント内包でオフライン閲覧できる。データ/制御/CI/観測の線種は各図の凡例を参照。[PNG出力・再生成・出典](diagrams/README.md)

## 配信経路

| 経路 | 構成 |
| --- | --- |
| アプリ | `app.rangleplay.stream` → HTTPS LB → serverless NEG → Cloud Run `rangle-stg`。`/` landing、`/demo/` Flutter、`/api/` Go。CDN無効 |
| メディア | `media.rangleplay.stream` → 同じLB → Cloud CDN → private GCS。モデルは不変URL・TTL24時間、LIVE indexはTTL1秒 |
| カタログ | private GCSの`snapshots/<sha256>.json`をGoへread-only FUSE mount。catalogだけversioning無効 |
| DNS | Cloudflareのapp/media A recordはGoogle LBへ向けた[DNS only](https://developers.cloudflare.com/dns/proxy-status/) |

公開RunはTokyo、1 vCPU/512MiB、request billing、service/revisionともmin0/max2、concurrency8、timeout30秒（暫定・反映済み）。API gateはinstanceごと8件・queue0で、UI/healthもplatformの同時要求枠を共有する。公開メディアは端末がCDNから取得・復号・描画し、Goは中継しない。生成・LIVEは対応する不変configとfeature flagで有効にする。

## ネットワーク境界とアクセス制御

**利用者が構成したVPC・subnetは未使用**。CustomJobのNetwork、RunのVPC connector/Direct VPC interfacesも未設定。図のCloud枠はサービスの所属を表す。

| 対象 | 境界 |
| --- | --- |
| 公開VIP | `34.96.101.8/32`（2026-11-10確認）。単一の公開IPで、subnet CIDRではない |
| 公開Run | `internal-and-cloud-load-balancing` ingress。外部入口はLBのみ。invoker IAMチェックは無効、操作認可はGo。既定run.app・候補tagへの外部直通は404 |
| 生成manager | ingress ALL＋Cloud Run IAM必須。公開principalなし。公開LBには載せず、業務APIはGoogle署名ID tokenのaudience/runtime SAも検証 |
| inputs/catalog | PAP enforced＋用途別IAM。匿名公開しない |
| media | private bucketをCDN cache-fill SAが読む。許諾済み素材のみ公開。Cloud Armorで公開prefixを制限 |
| Firestore/Vertex/AR | IAMで制御。private subnetによる隔離ではない |

通常・候補backendの両方にCloud Armorを付ける。`X-Rangle-Candidate: ci-candidate`は公開の経路選択子で、認証用の秘密ではない。[入口制限手順](stg-deployment.md#lb入口制限への切替)・[防御設定](security-hardening.md)

## 生成制御とデータの分離

同じimageを公開UI/APIとprivate生成managerの別mode・別SAで動かす。

| identity | 許可する操作 |
| --- | --- |
| `rangle-stg-runtime` | community DB `rangle-stg`、catalog読取、manager invoke。Vertex・生成DB・trainer actAsは不可 |
| `rangle-stg-generation` | 生成DB `rangle-stg-generation`、config/catalog読取、期限付きVertex操作・trainer actAs、進捗専用log view、所有資産削除/CDN無効化 |
| 専用同期SA | managerのactive同期・削除進行の有限処理。DB・Vertex権限なし |

公開側が運営/団体担当者のsession・現在の役割・CSRFを確認し、Google署名ID tokenと短寿命の操作主体assertionをmanagerへ渡す。主体assertion自体が独立署名JWTではなく、managerが指定の公開runtime identityを信頼して受け取る境界である。managerは所有者・入力割当・固定presetを検査し、全団体/運営でactive 1件・最大10 attempts・30分・期限を共有する。[認証契約](publisher-auth.md#団体の生成公開)・[生成基盤](generation-infrastructure.md)

managerも1 vCPU/512MiB、request billing、service max1・revision min0、concurrency20、timeout30秒。今回の実環境ではrevision templateのmaxは5で、service max1とは区別する。通常trafficは1revisionへ100%、公開runtime・同期・CI配備のSAだけが期限付きinvokerを持つ。台帳のactive制限がGPU jobの同時実行枠を制御する。managerのconcurrencyはHTTP要求枠であり、GPU job数・視聴者数・生成性能の保証ではない。

団体の実削除は運営に限定する。公開Goの認可後、managerで所有ジョブを確定して公開registryから除外し、ジョブ終了確認→所有するGCS資産のgeneration一致削除→固定hostのCDN cache無効化を進める。管理画面の確認と既存の2分同期が有限量ずつ継続し、監査記録・共有入力・取得済みブラウザ内コピーは別管理とする。[削除契約](publisher-auth.md#団体削除)

GPUはTokyo Spot T4×1の有限CustomJob。入力は校正・同期済み複数カメラ録画とseedで、単眼upload・RTMP/RTSP受信は含まない。提供校正・seedの事前情報、破棄window、**継続30秒遅延の未達**は[クラウドLIVEの実測](cloud-live.md#2026-11-09の実gpu検証)に記載する。native mobile性能・大規模視聴・実GaussianのABR切替は未検証。

## ブラウザの配信・cache

GoはfeedとmanifestのURLを返し、productionのmanifest/scene/model/video実体はCDN/GCSから配信する。UIのJS/Wasm等はGoのbuild時gzip sidecarで配り、media backendの圧縮は無効のまま。RGS1-Z1内部のgzipとは別である。

公開VODのtransportでは[JS](../apps/web/viewer/src/transport-playback.js)が取得・size/SHAを検証し、ArrayBufferをWorkerへ渡す。検証済みの`assets/`segmentだけを[Cache Storage](../apps/web/viewer/src/segment-cache.js)の固定2枠・各16MiB以下・合計32MiB、最大10分で再利用し、hitはABRの転送推定に混ぜない。LIVE/generatedや非公開モデルはこのcacheの対象外で、LIVE等の互換経路はWorkerが直接取得する。Workerは復号・時間ソートを担い、WebGLはmain threadで描画する。ABR接続は実装済みだが、実複数品質ラダー・実機QoEの受入は未完了。

通常動画とseek用thumbnailは専用のHTML videoで処理する。thumbnailは非表示またはdecode完了後1秒idleで専用動画を解放し、最大16JPEGを保持する。既定feedと、管理者専用private snapshot画面・有限のsnapshot配信比較画面は分けて扱う。private snapshotだけは毎回の管理者認可後、Goがprivate/no-storeで配信する。実資産の配置は今回再確認していない。[端末予算](https://github.com/Hosi121/rangle/issues/29)・[配信契約](transport-playback.md)

## ログ監視の経路と到達点

Run、Vertex worker、LB/CDN、Scheduler/Workflowsの4系統を専用sinkで選び、TokyoのLogging bucket `rangle-stg-logs`へ30日保持する。Cloud Storage bucketとは別資源で、監査ログは対象外。既存`_Default`/`_Required`は変更しない。

MonitoringのログパネルとLogs Explorerは同じ`_AllLogs` viewを読む。公開Runの要求数/秒・revision別p95は標準メトリクスで、ログ由来でもブラウザ描画時間でもない。managerのログも収集する。別の`generation-progress` viewはVertexの`ml_job`/`workerpool0-0`へ絞り、IaCでmanagerへviewAccessorを付与する。このbinding自体は今回live再確認の対象外である。managerはジョブ所有者を確認し、実行中のstatic/dynamic学習の3種eventを集計した進捗・ETAだけを公開APIへ返す。LIVEは対象外で、ETAは学習残時間の推定であり、provisioning/uploadを含む完了時刻の保証ではない。ブラウザへrawログやproject全体の読取権限を渡さない。[検索・検証範囲](observability.md)・[出力方針](logging.md)

## 配備と停止

TerraformがIAM・mount・args・catalog・scaling、CIがimage/trafficを管理する。GitHub WIFで認証し、managerの認証付きcandidate→公開アプリのLB candidateの順で確認・昇格する。後続失敗時は両serviceのtrafficを戻し、DB/IAMは戻さない。[CI/CD](cicd.md)

15分ごとのguardと2分ごとの生成同期がENABLED。予算USD300・期限2026-11-14 15:00 UTCでScheduler/Workflowsがowned資源を停止する。保持データ、IAMの制約、通知遅延による費用上限の限界は[停止手順](stg-deployment.md#予算期限の停止)に集約する。

## 検証の到達点

既存の配備検証で、LB経由のUI/API、媒体のRange/CORS/ETag/hash、匿名origin拒否、WIF配備、manager分離、監査・ログ収集を確認済み。停止guardの通常実行は成功。破壊的な停止分岐・restore・端末性能・大規模視聴は未検証。[配備記録](security-review.md)

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

## 今回の照合範囲

Cloud Runのrevision/traffic/args/scaling・LB/NEG/CDN/Armor・manager invoker・2 DBのPITR/backup・Logging bucket/view・Scheduler/guardの実設定を照合した。今回の照合時の公開revisionは`rangle-stg-00107-2st`、managerは`rangle-stg-generation-00031-tj6`で同一image・各100%。図は毎回のrevision名ではなく役割と接続を示す。

Vertexのmachine/Spot/T4/1800秒/retry無効は既存jobの実specでも確認したが、現在GPUが常時動いているという意味ではない。trainer等の全IAM bindingは今回の監査対象外で、権限の細部はIaC/認証契約に基づく。CloudflareのDNS-only設定は既存記録由来で、今回app/mediaのA recordが同じGoogle LB VIPへ解決することを確認した。クラウドへの変更・ジョブ起動・デプロイは行っていない。
