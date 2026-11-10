# rangle のクラウド構成

発表用presentation環境の構成。2026-11-10 **09:14–09:19 UTC**の実設定と`stg`の実装に基づく。正本は[presentation module](../infra/gcp/modules/presentation/)と[配備手順](stg-deployment.md)。

| 図 | 内容・実装 |
| --- | --- |
| [全体](diagrams/overview.html) | Flutter Web、Go、Vertex AI、CDN、CI/CD、横断するログ監視 |
| [ML処理](diagrams/ml-pipeline.html) | 運営/団体の認証→Generation Manager→生成DB→[CustomJob](../internal/generation/vertex.go)→掲載/削除 |
| [配信処理](diagrams/delivery.html) | feed/manifest URL・LIVE index→CDN→JS取得/ABR・端末cache→[Worker復号](../apps/web/viewer/src/temporal-worker.js)→WebGL |
| [ログ監視](diagrams/observability.html) | 4種の収集元→専用Logging bucket、標準指標→Monitoring、進捗専用view→manager |

HTMLはアイコン・フォント内包でオフライン閲覧できる。データ/制御/CI/観測の線種は各図の凡例を参照。[PNG出力・再生成・出典](diagrams/README.md)

## 配信経路

| 経路 | 構成 |
| --- | --- |
| アプリ | `app.rangleplay.stream` → HTTPS LB → serverless NEG → Cloud Run `rangle-stg`。`/` landing、`/demo/` Flutter、`/api/` Go。CDN無効 |
| メディア | `media.rangleplay.stream` → 同じLB → Cloud CDN → private GCS。モデルはImmutable URL・TTL24時間、LIVE indexはTTL1秒 |
| カタログ | private GCSの`snapshots/<sha256>.json`をGoへread-only FUSE mount。catalogだけversioning無効 |
| DNS | Cloudflareのapp/media A recordはGoogle LBへ向けた[DNS only](https://developers.cloudflare.com/dns/proxy-status/) |

公開RunはTokyo、1 vCPU/512MiB、request billing、service/revisionともmin0/max2、concurrency8、timeout30秒。API gateはinstanceごと8件・queue0で、UI/healthもRunの同時要求枠を共有する。生成・LIVEはSHAで固定したconfigとfeature flagで有効にする。

## 入口とアクセス制御

| 対象 | 境界 |
| --- | --- |
| 公開VIP | `34.96.101.8`（2026-11-10確認） |
| 公開Run | `internal-and-cloud-load-balancing` ingressでLB経由に限定。invoker IAMチェックは無効、操作認可はGo |
| 生成manager | ingress ALL＋Cloud Run IAM。業務APIでもGoogle署名ID tokenのaudienceとruntime SAを検証 |
| inputs/catalog | Public Access Preventionと用途別IAM |
| media | private bucketをCDN cache-fill SAが読む。許諾済み素材のみ公開。Cloud Armorで公開prefixを制限 |
| Firestore/Vertex/AR | 用途別IAMで制御 |

通常・候補backendの両方にCloud Armorを付ける。`X-Rangle-Candidate: ci-candidate`で候補backendを選択し、操作認可には通常と同じ認証を使う。[入口制限手順](stg-deployment.md#lb入口制限への切替)・[防御設定](security-hardening.md)

## 生成制御とデータの分離

公開UI/APIとGeneration Managerは、同じimageを別mode・別SAで動かす。

| identity | 許可する操作 |
| --- | --- |
| `rangle-stg-runtime` | community DB `rangle-stg`、catalog読取、manager invoke |
| `rangle-stg-generation` | 生成DB `rangle-stg-generation`、config/catalog読取、期限付きVertex操作・trainer actAs、進捗専用log view、所有資産削除/CDN無効化 |
| 専用同期SA | managerのactive同期・削除進行APIの呼出 |

公開Goが運営/団体担当者のsession・現在の役割・CSRFを確認する。managerはGoogle署名ID tokenで公開runtimeを認証し、そのruntimeが付けた短寿命の操作主体assertionを受け取る。所有者・入力割当・固定presetを検査し、全団体/運営でactive 1件・最大10 attempts・30分・期限を共有する。[認証契約](publisher-auth.md#団体の生成公開)・[生成基盤](generation-infrastructure.md)

managerは1 vCPU/512MiB、request billing、service max1（revision templateはmin0/max5）、HTTP concurrency20、timeout30秒。公開runtime・同期・CI配備のSAに期限付きinvokerを付与する。GPU jobの同時実行枠は台帳のactive制限で管理する。

団体の実削除は運営に限定する。公開Goの認可後、managerが所有ジョブを確定して公開registryから除外し、ジョブ終了確認→所有するGCS資産のgeneration一致削除→固定hostのCDN cache無効化を進める。管理画面の確認と2分ごとの同期で継続する。監査記録・共有入力・取得済みブラウザ内コピーは別管理とする。[削除契約](publisher-auth.md#団体削除)

GPUはTokyoのSpot T4×1をCustomJobで起動する。LIVEは校正・同期済みの複数カメラ録画とseedを使い、元のPTSに沿って更新・公開する。入力条件や遅延は[クラウドLIVEの実測](cloud-live.md#2026-11-09の実gpu検証)を参照。

## ブラウザへの配信とcache

公開視聴ではGoがfeedとmanifestのURLを返し、端末がmanifest/scene/model/videoをCDNから取得する。UIのJS/Wasm等はbuild時にgzip sidecarを生成し、Goが配信する。RGS1-Z1はコンテナ内部で圧縮し、media backendのHTTP圧縮は無効にしている。

公開VODでは[JS](../apps/web/viewer/src/transport-playback.js)がsegmentを取得し、size/SHAを検証してArrayBufferをWorkerへ渡す。[Cache Storage](../apps/web/viewer/src/segment-cache.js)は公開`assets/`の検証済みsegmentを固定2枠・各16MiB以下・合計32MiB、最大10分で再利用する。ABRの転送推定にはnetwork取得だけを使う。LIVE/generated・非公開モデルはこのcacheの対象外で、LIVE等の互換経路はWorkerが直接取得する。Workerが復号・時間ソート、main threadのWebGLが描画を担う。

通常動画とseek用thumbnailは専用のHTML videoで処理する。thumbnailは非表示またはdecode完了後1秒idleで専用動画を解放し、最大16JPEGを保持する。通常のfeedとは別に、管理者用private snapshot画面とsnapshot配信比較画面を用意する。private snapshotは毎回の管理者認可後、Goがprivate/no-storeで配信する。[端末予算](https://github.com/Hosi121/rangle/issues/29)・[配信契約](transport-playback.md)

## ログと監視

Run、Vertex worker、LB/CDN、Scheduler/Workflowsの4系統を専用sinkで選び、TokyoのLogging bucket `rangle-stg-logs`へ30日保持する。監査ログと既存の`_Default`/`_Required`は従来の設定を維持する。

MonitoringのログパネルとLogs Explorerは同じ`_AllLogs` viewを読む。公開Runの要求数/秒・revision別p95はCloud Runの標準メトリクスを使う。

進捗APIでは、managerが専用の`generation-progress` view（Vertexの`ml_job`/`workerpool0-0`）を読む。ジョブ所有者を確認し、実行中のstatic/dynamic学習の3種eventをquery・集計して進捗と学習残時間のETAを返す。LIVEは対象外。[検索・検証範囲](observability.md)・[出力方針](logging.md)

## 配備と停止

TerraformがIAM・mount・args・catalog・scaling、CIがimage/trafficを管理する。GitHub WIFで認証し、managerの認証付きcandidate→公開アプリのLB candidateの順で確認・昇格する。後続失敗時は両serviceのtrafficを戻し、DB/IAMは戻さない。[CI/CD](cicd.md)

15分ごとのguardと2分ごとの生成同期が稼働する。予算USD300・期限2026-11-14 15:00 UTCを条件に、Scheduler/Workflowsが管理対象の資源を停止する。通知遅延やデータ保持を含む運用条件は[停止手順](stg-deployment.md#予算期限の停止)を参照。

## 参照した一次資料

[LB/serverless NEG](https://docs.cloud.google.com/load-balancing/docs/https/setup-global-ext-https-serverless)・[CDN/private origin](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket#make_your_cloud_storage_bucket_private)・[GCS mount](https://docs.cloud.google.com/run/docs/configuring/services/cloud-storage-volume-mounts)・[WIF](https://docs.cloud.google.com/iam/docs/workload-identity-federation-with-deployment-pipelines)・[Budget通知](https://docs.cloud.google.com/billing/docs/how-to/budgets-programmatic-notifications)

## 今回の照合範囲

Cloud Runのrevision/traffic/args/scaling・LB/NEG/CDN/Armor・manager invoker・2 DBのPITR/backup・Logging bucket/view・Scheduler/guardの実設定を照合した。公開revisionは`rangle-stg-00107-2st`、managerは`rangle-stg-generation-00031-tj6`で、同一imageへ各100%のtrafficを向けていた。Vertexのmachine/Spot/T4/1800秒/retry無効は既存jobのspecで確認した。

進捗viewのmanager向けviewAccessorとtrainer等のIAM bindingはIaC/認証契約に基づき、今回の実環境照合には含めていない。private snapshot資産の配置も対象外。CloudflareのDNS-only設定は既存記録に基づき、今回はapp/mediaのA recordが同じGoogle LB VIPへ解決することを確認した。

配備・検証履歴は[配備記録](security-review.md)を参照。
