# rangle ABRと配信最適化の調査

2026-11-07の初期調査。ABRは次に取得する品質の選択で、圧縮・区間分割・cache・先読みと組み合わせる。実Gaussianへの接続状況は[現状](../backend-status.md)、HTTP仕様は[API](../api-delivery.md)を参照。

## 初期の選定

| 層 | 初期方針 |
| --- | --- |
| preview動画 | HLS/DASHと既存playerを候補にする |
| Gaussian | 共通時間軸の品質別・独立区間HTTP object |
| ABR | 帯域＋bufferに復号・メモリ制約を加え、Goで基準実装 |
| 配信先 | 版付きURL、Cache-Control/ETag/Range、Storage/CDN |

[Media3の対応形式](https://developer.android.com/media/media3/exoplayer/supported-formats)はcontainer/codecの条件を持つ。Gaussianバイナリにplaylistを付けても自由視点描画はできず、専用decoder/viewerが必要。

## ABRの選択肢

| 方法 | 役割 |
| --- | --- |
| 固定品質 | 比較基準 |
| Throughput | 転送履歴から選ぶ小さな基準実装 |
| Buffer | bufferと品質効用。長い連続再生で比較 |
| Hybrid | 帯域・buffer・切替履歴を併用 |
| 描画制約付き | 復号時間・メモリ・frame dropを加える |

[dash.js ABR](https://dashif.org/dash.js/pages/usage/abr/)・[設定](https://dashif.org/dash.js/pages/usage/abr/settings.html)を参照。[BOLA](https://arxiv.org/abs/1601.06748)は別の定式化であり、bufferを使う独自規則をBOLA実装とは呼ばない。

## 帯域推定

[Shakaの推定器](https://github.com/shaka-project/shaka-player/blob/main/lib/abr/ewma_bandwidth_estimator.js)を参考に、時定数の異なるEWMAの小さい値を採る。正常完了したnetwork取得だけを使い、端末cache・失敗・中断は混ぜない。[Shakaの短時間sample除外](https://github.com/shaka-project/shaka-player/blob/main/lib/abr/simple_abr_manager.js)

CDN hitも端末までのnetwork転送なので端末cacheとは別。小区間のRTTや並行接続の帯域共有も影響する。[調和平均等](https://dashif.org/dash.js/pages/usage/abr/throughput-calculation.html)は同一入力・品質規則で比較する。

## バッファと端末の制約

```text
準備時間 = ネットワーク取得時間 + 初期化・復号時間 + GPUへの転送時間
取得量   = 次区間のbytes + まだ取得していない初期化資産のbytes
```

[InsufficientBufferRule](https://dashif.org/dash.js/pages/usage/abr/insufficient-buffer-rule.html)を参考に、実サイズと準備時間をbuffer内へ収める。取得済みbytesと描画可能な秒数は区別する。

Go初期実装は復号時間・作業メモリをmetadata上の上限として扱う。実機では展開後属性、旧/新model・先読みの同時保持、GPU時間・熱状態も測る。[DroppedFramesRule](https://dashif.org/dash.js/pages/usage/abr/dropped-frames-rule.html)

## 品質ラダーと切り替え境界

同じ時間軸・座標系で、初期化情報とGaussian対応関係を定義し、独立復号可能な区間で切り替える。品質とbitrateの比例や、異なる品質の差分混在は仮定しない。分割で増える基準情報・要求数も測る。

[RFC8216](https://www.rfc-editor.org/rfc/rfc8216)・[Apple authoring](https://developer.apple.com/streaming/hls-authoring-specification-for-apple-devices.html)の境界整合を参考にするが、独自JSON manifestはHLS準拠ではない。

## 短いフィードで優先する最適化

1. 起動時は低品質で開始し、現在clipを優先する。
2. 次clipの先読み量・並行数を制限し、離脱時に取得/復号取消・資源解放する。
3. 遅い取得は転送済み量と再取得量を含めて継続/中断を判断する。
4. 帯域回復時はbufferと余裕を確認して上げる。

[中断規則](https://dashif.org/dash.js/pages/usage/abr/abandon-request-rule.html)・[履歴規則](https://dashif.org/dash.js/pages/usage/abr/switch-history-rule.html)を参照。品質選択と取得schedulerは別部品。区間0.5/1/2秒を例に、一括取得とも比較する。[BOLA360](https://arxiv.org/abs/2309.04023)の視点優先は発展候補で、Gaussianでは遮蔽・視点移動時の不足も評価する。

## CDNとHTTP

| 項目 | 条件 |
| --- | --- |
| cache | 内容版付きURL・明示Content-Type/Cache-Control・強いETag。公開bytesは上書きしない |
| Range | 分割objectとも比較。originのheaderとcache fill条件を実CDNで確認 |
| 圧縮 | 保存packingとHTTP Content-Encodingを分ける。動的圧縮時のRange無効化・ETag弱化に注意 |
| 接続 | HTTP/2・HTTP/3、再利用・並行数を比較。client側HTTP/3とbackend protocolは別 |
| 計測 | CMCD等を導入する場合もsession値で共有cache keyを分断しない |

初期serverはqueryによる資産の別名要求を拒否し、CMCDは未収集。出典：[cache](https://docs.cloud.google.com/cdn/docs/caching)・[Range](https://docs.cloud.google.com/cdn/docs/caching#byte_range_requests)・[動的圧縮](https://docs.cloud.google.com/cdn/docs/dynamic-compression)・[HTTP/3](https://docs.cloud.google.com/load-balancing/docs/https#http3_support)・[CMCD](https://dashif.org/dash.js/pages/usage/cmcd.html)

## 比較計画

同一データ・品質で、全量/固定品質→区間/固定品質→Throughput ABR→buffer併用→復号/メモリ制約付きの順に効果を分離する。

記録する値はpreview/自由視点TTFF、停止回数/時間、品質推移/切替数、未使用bytes、RAM/VRAM、復号・frame-time。帯域/RTT/損失と端末/CDNのcache状態を揃える。画質を落とした結果を同品質の高速化とせず、境界・ちらつき・ボール/手足も評価する。

## 今回の実装範囲

[Goの基準契約](../streaming-contract.md)に沿うABR・合成simulator・HTTP serverまで。実品質ラダー、取得中断/先読みclient、BOLA、HLS/DASH化、実端末性能の成立はこの初期調査に含めない。
