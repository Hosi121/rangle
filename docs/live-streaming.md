# 自由視点ライブの配信

校正・同期済み複数カメラの到着画像から4DGSを逐次更新し、短いwindowごとに公開する。Flutterは視点を保って更新する。約30秒を目安に実測で画質/頻度を判断し、30秒以内・全区間欠損なしを保証しない。

## 入力から視聴まで

1. workerは34本の録画を元PTSで流し、同期した到着画像だけをbounded ringへ渡す。待機は最新window1つで、遅れは数えてskipする。
2. Gaussian/optimizerを引き継ぎ、session中の校正・正規化・cameraを固定する。model・local評価・sceneを新規作成後、local indexをatomic replacementする。
3. publisherが参照/SHA/RGS1/時刻/cameraを検証し、model/sceneをGCSへcreate-only保存して実CDN bytesを照合する。
4. 不変検証済みindexをprivate bucketへ保存し、公開session pointer/private discoveryを各GCS generation指定で更新する。競合は停止し他producerを上書きしない。
5. Goが一覧を返し、公開FlutterはCDNからsession更新を取得する。同じiframeへ切り替え、新model準備まで旧modelを操作できる。

生画像・NPZ・学習状態・local metricsは非公開。公開indexはsource・区間・サイズ/SHA・capture/availability時刻・集計品質だけ。[入力/学習/検証](live-training.md)

## HTTPと保存先

| 経路 | 契約 |
| --- | --- |
| `GET /api/v1/live` | private `live/index.json` の一覧。Goの1秒共有cacheで同時readをまとめる |
| `GET /api/v1/live/{id}` | session詳細、localの更新取得先 |
| `https://media.…/live-index/{id}.json` | 同じJSON、専用CDN1秒cache。公開構成のpoll先 |
| `https://media.…/live/{id}/{sequence}-{modelSHA16}/scene.json` | 不変scene。同directoryにmodel.rgs |
| `/live-assets/{id}/{revision}/…` | 指定local rootのscene/modelだけ。Range対応 |

一覧/API詳細はGET/HEAD・ETag/304、失敗は503/no-store。上限8sessions・各32segments・index512KiB・model32MiB。未設定一覧は空。視聴用認証を追加せずproducer資格情報をbrowserへ渡さない。

既存mediaは24時間cacheを維持し、`/live-index/*` だけ別backend bucketの1秒policyへ送る。同じGCS bucket/cache-fill identityを使い、新bucket・GPU・公開origin権限は作らない。API poll削減の設計であり、10万人の容量/費用の実証ではない。

## 起動と公開

既存serverへ追加する。rootはworkerの `live-assets` directory。

```sh
rangle-server ... -live-index /path/to/output/index.json \
  -live-assets-root /path/to/output/live-assets
go build -o /path/to/private/rangle-live-check ./cmd/rangle-live-check
```

publisherは既定local検証のみで認証/cloudを呼ばない。workerの初回index後に起動し、`--execute` で保存、`--follow` で追従、`--max-seconds` で区切る。

```sh
python3 scripts/publish-live.py \
  --gate /path/to/private/rangle-live-check \
  --index /path/to/output/index.json --root /path/to/output \
  --receipts /path/to/private/receipts \
  --origin https://media.rangleplay.stream \
  --project comin-up --media-bucket rangle-stg-113704194486-media \
  --catalog-bucket rangle-stg-113704194486-catalog \
  --account YOUR_OPERATOR_ACCOUNT --environment presentation-stg \
  --approved-source https://huggingface.co/datasets/BestWJH/VRU_Basketball \
  --execute --follow --max-seconds 600
```

sourceは承認済みURLへ固定する。同じURLのbytesを変えず他sessionを維持し、終了sessionの再開は新IDを使う。media後/discovery前の中断も公開pointerのproofで再開可能。競合時は最新状態を確認し同じ入力で再実行する。古いsession/model削除は別運用。

本番は対応image配備後 `live_streaming_enabled=true`。固定private objectは `gs://…-catalog/live/index.json` で、FUSE metadata cacheを避け既存read-only identityによるHTTP取得を使う。Flutterの `RANGLE_LIVE_MEDIA_ORIGIN` は承認originへ固定し、空ならsame-origin API。既存期限・試行数・費用上限は維持する。

## 測定の範囲

`captured_at` はsession実時間開始+PTS、`available_at` はlocal出力完了/cloud media検証後。初期化・学習・評価・梱包・公開を分ける。実描画はさらにpoll/cache・転送・復号・GPU準備を要し、HTTP availabilityとは区別する。

初期点群/校正は事前情報で、完全な幾何因果性を主張しない。同じ解像度/評価カメラのPSNR/SSIMと画像で品質を記録し、VOD12,000stepをlive設定へ置き換えない。一般単眼・RTMP/RTSP接続管理・常駐cloud GPU・欠損なし25fps・実端末表示/画質・大規模負荷は対象外。

10秒の録画入力で4modelがCDNへ届き、最大capture→公開29.810秒、observer取得30.236秒、EOF前更新を確認した。短区間の断続更新で、画面表示/長時間保証ではない。[条件と品質](live-training.md#verified-cloud-delivery-2026-11-09)、[最後のmodel](https://app.rangleplay.stream/demo/?live=basketball-live-20261109)。

参考：[3DGStream](https://arxiv.org/abs/2403.01444)、[Instant Gaussian Stream](https://github.com/yjb6/IGS)。既存RGS1/gsplatのbaselineで、これらの再現・論文性能達成は主張しない。
