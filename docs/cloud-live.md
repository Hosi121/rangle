# クラウドGPUによるライブ自由視点生成

校正済み多視点映像の到着に合わせて4DGSを更新し、完成モデルをCDNへ逐次公開する。現入力はVRU Basketball gzの録画を元PTSで供給する10秒テスト。実カメラのRTMP/RTSP受信は未対応。[構成図](diagrams/ml-pipeline.html)・[配信契約](live-streaming.md)

## 実行と停止

既存生成APIへlive presetとrequest IDを送る。受付時から`viewer_url=/demo/?live=<job ID>`を返し、認証・所有者確認・再送・中止は[生成API](generation.md)と共用する。live結果はVOD掲載不可。

- 静的・動的・liveで同じ生成台帳を共有し、同時1件・累計10 attempts・1件30分。失敗・中止でも枠を戻さない。
- Tokyo Spot T4×1。offlineは4 vCPU、liveは16 vCPU、worker×1、`disableRetries=true`。別region/GPU/attemptへ自動切替しない。
- 起動待ちも含む絶対期限をworkerへ渡す。container再起動対策として`live/snapshots/<session>/producer.json`をcreate-only claimし、二重producerを拒否する。

30秒目標は入力開始後の遅延。GPU確保・image取得・入力準備は別計測する。

## データと配信

1. `ml/rangle_live/bundle.py`で校正・seed・動画・manifestをchecksummed ZIPへまとめ、private inputsの`datasets/live/`へ新規作成する。圧縮前後1GiB・256 files上限。展開時にtraversal・symlink・重複・未承認member・SHA/size不一致を拒否する。
2. workerがADCで取得し、PTSまで待ってdecodeする。処理待ちは最新window 1件だけ。到着画像でGaussian/optimizerを更新する。提供校正・seedの事前情報と画質条件は[ライブ学習](live-training.md)を参照。
3. 追加量子化・間引きをせず既存RGS1と可逆packingを使う。Go validatorでscene・hash・time range・camera・出典を検証し、不変`live/<session>/<sequence>-<hash>/`へmodel→sceneの順で配置する。
4. 実CDNのbytes確認後、公開`live-index/<session>.json`と非公開`live/index.json`をgeneration CAS更新する。他session・sequence・同一revisionのbytesを上書きしない。

[GCSの同名object書込上限](https://docs.cloud.google.com/storage/quotas#objects)に合わせ、公開成功後1.25秒待ち、その間の更新を最新1件へ集約する。終了・異常時も同じpacingを使う。429はGETまたは`ifGenerationMatch`付きPOSTだけを、同一body/CAS・絶対期限内で最大4試行する。

indexのCDN TTLは1秒、modelは不変URLで長期cache。viewerは旧モデルと視点を保って交換し、iframeを再読み込みしない。

## 権限と配備

生成configに`live: {image, service_account, catalog_bucket}`を追加し、同一project・Tokyoのimage digestを固定する。`application.generation_live_enabled`は既定falseで、生成configと`live_streaming_enabled`が必要。

専用live-trainerには期限付きで以下だけを許可する。既存offline trainerは変更しない。

| 対象 | 権限 |
| --- | --- |
| inputs `datasets/live/` | get |
| media `live/`、catalog `live/snapshots/` | get/create |
| media `live-index/`、catalogの厳密な`live/index.json` | get/create/delete（上書きinsertに必要） |

bucket全体のlist・任意model削除・Firestore・IAM変更は不可。[IAM実装](../infra/gcp/modules/presentation/generation-live.tf)・[生成基盤](generation-infrastructure.md)・[上書きAPI](https://docs.cloud.google.com/storage/docs/json_api/v1/objects/insert)

```bash
docker build --platform=linux/amd64 --target live-trainer -f ml/Dockerfile .
```

Torch 2.6/CUDA 12.4のdigest、T4用gsplat、OpenCVのhash、Go validatorを固定する。defaultのoffline `trainer` targetは互換を維持し、workerは利用者tokenやgcloudを使わない。

配備順：Go/UI・worker image → 入力bundle → 限定IAM → immutable configとSHA選択 → candidate確認・昇格。GPU検証は受付APIを通し、terminalとactive解放まで確認する。IAM role作成直後の伝播エラーはrole/projectを確認して再planし、広いrole付与やstate/台帳初期化で回避しない。

34カメラの復号に16 vCPU・decoder16並列を使う。[32 vCPUではT4が2枚以上必要](https://docs.cloud.google.com/vertex-ai/docs/training/configure-compute#GPUs)なため採用しない。当時のTokyo通常価格による16 vCPU＋T4＋100GiB diskの30分参考額は約USD0.78で、USD2/attempt予約内。実請求のhard capではない。[価格](https://cloud.google.com/vertex-ai/pricing)

## 2026-11-09の実GPU検証

同じVRU gz・34カメラ・10秒・25fps、学習480×270、30学習視点＋4評価視点、提供点群、300更新/windowで比較した。元動画は1920×1080 HEVC。bundleは783,744,881 bytes、SHA256 `133e9afdc4caded541dd4040d4caf0cf442ffdfa71c3c4b161eabbc343aef50b`。

| 測定 | 初回・4 vCPU | 現設定・16 vCPU＋公開pacing |
| --- | ---: | ---: |
| 入力開始→最初のモデルを実HTTP確認 | 53.62秒 | 30.88秒 |
| 各window終端→実HTTP確認の平均／最大 | 139.07／220.77秒 | 48.15／63.04秒 |
| 10秒分の入力を受信し終えるまで | 223.20秒 | 64.31秒 |
| 公開モデル数／破棄window数 | 13／0 | 8／5 |
| 累積optimizer更新数 | 3,900 | 2,400 |

現設定sessionは[`33b4663b4e936e0e50104d192af37b70`](https://app.rangleplay.stream/demo/?live=33b4663b4e936e0e50104d192af37b70)。初回modelのmetadata上はwindow終端から28.77秒、model/scene/indexの実HTTP確認は30.08秒、入力開始からはwindow長0.8秒込み30.88秒。別observerでは36.24〜64.94秒だった。表はworkerのmonotonic時計でCDN確認までを測り、Flutterのpoll・復号・描画は含まない。

受付→入力開始は313.31秒。worker内の取得・展開22.60秒、準備全体28.75秒で、残りはVertex確保・image取得等。開始ボタンから30秒で表示できる結果ではない。

CPUとpacingを同時に変更し、時間窓・更新数も異なるため、CPU単独効果や画質改善とは扱わない。最終packed PSNR/SSIMは現設定21.23 dB/0.7226、初回21.84 dB/0.7457。各modelは4.40〜5.82 MB。

途中の16 vCPU試行はpointer連続更新の429で失敗。pacing・条件付きretry・producer claimの追加後、1確認実行で8モデルのSHA・camera・RGS1復号と時刻別の有限位置/可視点をproduction JS decoderでも検証した。Vertex succeeded、子process exit0・回収済み・強制killなし。台帳used=7/activeなし、失敗込み3 attempts消費、確認用accountは削除済み。

**30秒遅延の継続達成は未達**。長時間入力・実カメラ・端末描画・大規模視聴は未検証。[#89](https://github.com/Hosi121/rangle/issues/89)・[次の品質/圧縮候補](research/gs-quality-compression-2026.md)
