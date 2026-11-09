# 自由視点ライブの配信

校正・同期済みの複数カメラ動画を実時間で入力し、到着済みの画像から4DGSを逐次更新する。短い時間窓ごとにモデルを公開し、Flutterはカメラ操作を保ったまま更新する。まず約30秒の遅延を目安に、実測から画質と更新頻度を判断する。30秒以内の保証や、映像全区間を欠けずに配る実装ではない。

## 入力から視聴まで

1. ローカルworkerが34本の録画を元のPTSで流す。同期した到着画像だけを有界ringへ渡し、処理待ちは最新window一つにする。遅れたwindowは数を記録して飛ばす。
2. 同じGaussianパラメータ・optimizerを次のwindowへ引き継ぐ。校正・正規化・cameraはsession中固定。`model.rgs`、ローカル評価、`scene.json`を新規作成し、最後にlocal indexをatomic replacementする。
3. publisherがindex・参照・SHA・RGS1・sceneの時刻とcameraを検査する。モデルとsceneをGCSへcreate-onlyで保存し、実CDNのbytesを照合する。
4. immutableな検証済みindexをprivate bucketへ保存する。公開session pointerとprivate discovery indexを、それぞれGCS generationを指定して更新する。競合時は停止し、他producerの更新を上書きしない。
5. Goはsession一覧を提供する。公開環境のFlutterはsession更新をCDNから取得し、同じiframe内で新しいモデルへ切り替える。旧モデルは新しいモデルの準備完了まで操作できる。

生画像・NPZ・学習状態・ローカルmetricsは公開しない。公開indexにはsource、区間、サイズ・SHA、capture/availabilityの時刻と集計品質だけを含める。workerの入力・設定・検証手順は[ライブ学習](live-training.md)を参照。

## HTTPと保存先

| 経路 | 内容・キャッシュ |
| --- | --- |
| `GET /api/v1/live` | session一覧。Goはprivate `live/index.json`を読み、1秒の共有cacheで同時readをまとめる |
| `GET /api/v1/live/{id}` | session詳細。ローカルの更新取得にも使う |
| `https://media.…/live-index/{id}.json` | 同じsession JSON。専用CDN policyで1秒cache。公開環境の定期取得先 |
| `https://media.…/live/{id}/{sequence}-{modelSHA16}/scene.json` | immutableなscene。モデルは同じdirectoryの`model.rgs` |
| `/live-assets/{id}/{revision}/…` | 明示したローカルrootからの開発用配信。scene/modelだけを許可し、Rangeにも対応 |

一覧・API詳細はGET/HEAD・ETag/304に対応し、read/検証失敗は503・no-store。8sessions・各32segments・index512KiB・各model32MiBに制限する。未設定時の一覧は空配列。viewing用の追加認証は設けず、producerの資格情報をブラウザへ渡さない。

既存mediaの24時間cacheは維持する。`/live-index/*`だけを別backend bucketの1秒policyへルーティングする。同じGCS bucketとcache-fill identityを使い、新しいbucket・GPU・公開origin権限は追加しない。短いindexのCDN化はAPIの定期取得を減らす設計であり、10万人の容量や費用を実証したものではない。

## 起動と公開

ローカルでは既存serverに次を追加する。`live-assets-root`はworker出力の`live-assets`directoryを選ぶ。

```sh
rangle-server ... -live-index /path/to/output/index.json \
  -live-assets-root /path/to/output/live-assets
go build -o /path/to/private/rangle-live-check ./cmd/rangle-live-check
```

publisherは既定でlocal検証だけを行い、認証やcloud APIを呼ばない。`--execute`で保存し、`--follow`でlocal index更新を追う。`--max-seconds`で今回の実行を区切る。初回indexをworkerが作ってから起動する。

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

sourceはoperatorが承認済みのものへ固定する。再実行時も同じURLのbytesを変更せず、他sessionを維持する。終了したsessionの再開には新しいIDを使う。media公開後・discovery更新前に中断した場合も、公開session pointerの既存proofを使って再開できる。index競合は自動で上書きせず、最新状態を確認して同じ入力で再実行する。古いsession/モデルを削除する運用は別途必要。

本番は対応imageを先に配備してから、Terraformの`live_streaming_enabled=true`で有効にする。固定private objectは`gs://…-catalog/live/index.json`。GCS FUSEのmetadata cacheを避けるため、live indexは既存read-only identityで直接HTTP取得する。Flutterの`RANGLE_LIVE_MEDIA_ORIGIN`は承認したmedia originに固定し、空ならsame-origin APIを使う。既存の停止期限・GPU試行数・費用上限は変更しない。

## 測定の範囲

`captured_at`はsessionの実時間開始と入力PTSから求める。`available_at`はローカルでは出力完了、cloudではmedia検証後の時刻。初期化・学習・評価・梱包・公開を分けて記録する。実画面に描かれるまでの遅延には、さらにpoll/cache・転送・復号・GPU準備が加わる。HTTPで配信可能になった時間を実画面の表示時間とは呼ばない。

与えられた初期点群・校正は事前情報で、画像のオンライン更新が成立しても、幾何の完全な因果性は主張しない。短い学習では画質が下がる可能性があり、同じ解像度・評価カメラのPSNR/SSIMと実画像で記録する。既存VODの12,000step設定をライブ用設定へ置き換えない。

一般の単眼動画、RTMP/RTSPの接続管理、常駐cloud GPU、連続25fpsの欠損なし再構成、実端末の表示遅延・画質、大規模負荷は今回の成立確認に含めない。テスト入力は明示した録画ストリームである。

オンライン学習の参考には[3DGStreamの論文](https://arxiv.org/abs/2403.01444)と[Instant Gaussian Streamの公式実装](https://github.com/yjb6/IGS)がある。本実装は既存RGS1とgsplatを使う小さなbaselineで、これらの再現や論文の性能達成を主張しない。
