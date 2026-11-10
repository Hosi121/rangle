# 自由視点の連続再生

時間モデルを一度読み、同じmeshの時間uniformと描画順を更新する。再生中も視点を動かせ、同じmodel内のseekではHTTP取得/mesh交換をしない。対象は[共同学習モデル](dynamic-gs-training.md)で、一般4DGS互換は保証しない。manifest付きclipは[transport再生](transport-playback.md)から選択・検証して渡す。複数区間profileでは境界を跨ぐと独立modelを取得・交換し、RGS1自体のlocal時計は変えない。版付きHTTP資産として配信する。

## シーンとバイナリ形式

既存sceneのcamera/attributionを使い、framesを置き換える。

```json
{
  "frameRate": 25,
  "frameCount": 20,
  "temporal": { "url": "model.rgs" }
}
```

URLはmanifest最終URLから解決する。入力32MiB、frame数2〜15000、rateは正の有限値・240以下。header/manifestのframe数/rate、float有限性、scale/duration正値、opacity範囲を検証する。

RGS1はlittle endian、32byte header:

| offset | 型 | 内容 |
| --- | --- | --- |
| 0 | ASCII[8] | `RANGL4D1` |
| 8 | uint32 | Gaussian数 |
| 12 | uint32 | record stride、56固定 |
| 16 | uint32 | frame数 |
| 20 | float32 | frame rate |
| 24 | float32 | 最終時刻、`(frameCount - 1) / frameRate`秒 |
| 28 | uint32 | 予約、0固定 |

各Gaussianの56byte record:

| offset | 型 | 内容 |
| --- | --- | --- |
| 0 | float32[3] | 基準位置mu |
| 12 | float32[3] | 正のscale |
| 24 | uint8[3] | RGB |
| 27 | uint8 | 255、実際のalphaは時間評価で決定 |
| 28 | uint8[4] | 正規化quaternionのwxyz、`q * 128 + 128`を量子化 |
| 32 | float32[3] | 速度 |
| 44 | float32 | 時間の中心tau |
| 48 | float32 | 正の時間幅duration |
| 52 | float32 | peak opacity、0〜1 |

[可逆RGS1-Z](gaussian-compression.md)は復号後全byteが一致し、同じ描画経路を使う。モデル時刻tは物理秒/最終時刻。位置は `mu + velocity * (t - tau)`、alphaは `peak * exp(-0.5 * ((t - tau) / duration)^2)`。scale/回転/色は一定。alphaはsnapshot同様8bit ties-to-evenで丸め1未満を除外する。初期透明Gaussianも保持し、追加間引きしない。RGB/quaternion量子化はfloatに対して可逆ではない。

## 描画と資源管理

Workerが取得/検証/初回bufferを作り、固定ID順buffer/motion textureをtransferする。固定SDKのpreallocateUncompressed/parserでIDを保ち、meshを1回構築して標準static sort workerを終了する。shader変更はsource2か所一致を必須とし、SDK変更時は失敗する。

再生Workerは移動中心/alphaで対象を選び、float32深度キーの4radix passで奥→手前にsortする。同時1件、index bufferをtransfer/再利用する。mainは同じ時刻/cameraの結果を適用し、希望cameraを別保持して古いsortを新cameraへ使わない。位置/alpha以外のtextureは更新しない。

時計はsource rateで最終frameの表示時間まで含みloopする（20frame/25fpsは0.8秒周期、最終姿勢0.76秒）。描画速度で時計を遅らせない。seekは旧結果を無効化して最新要求へ集約し、通常進捗は最大10Hz、毎frame ready通知しない。

初回実GPU描画を待って表示する。[没入UI/動画引継ぎ](immersive-ui.md#切り替えの契約)に従い、再生button・double click/tapで再生停止する。drag/複数指をdouble tapと区別する。動画復帰後はWorker/GPUを停止保持し同項目で再利用する。非表示時間を再生位置へ加算しない。保持は1項目、取消/移動/エラーでWorker・追加texture・viewerを解放する。旧splat列はseekのみ。

## ローカルで確認した範囲

同じ学習済みUVG-GS BlueSquatからCPU出力し、camera/評価時刻を固定した。

| model | Gaussian | 20snapshot合計 → RGS1 |
| --- | --- | --- |
| 候補 | 249,895 | 74,625,408 → 13,994,152byte |
| 基準 | 237,321 | 79,312,256 → 13,290,008byte |

両modelのframe0/10/19をCPU float32評価し、描画対象snapshot recordのbyte一致を確認した。Windows Edge・NVIDIA RTX PRO 500 Laptop・ANGLE D3D11、960×803/DPR1/基準高さ803、同一machine内HTTPで候補を約4秒測定した。

| 指標 | 観測値 |
| --- | --- |
| instanced draw | 60.0回/秒 |
| 時間/sort結果反映 | 49.8回/秒 |
| draw間隔p95/最大 | 17.3/17.5ms |
| 常駐/時刻別描画Gaussian | 249,895/約97,000〜138,000 |
| 定常texture転送・texture/buffer確保 | 0回 |
| model HTTP/splat取得 | 1/0回 |

draw回数と時刻反映は別指標で、index bufferSubData更新は残る。JS/shader/WebGLエラー0、loop/orbit/seek/再生停止を確認し、ログ/画像はlocal保持。同GPUの実アプリ390×844/1440×900でも両clipの遅延準備・切替・視点/seek・動画復帰・同iframe/Worker再利用・移動時解放を確認した。各model取得1回、再入追加0回、JS/console/HTTPエラー0。

旧snapshot表示との同camera/frame0/10/19比較は平均絶対差0.047〜0.059（8bit）、PSNR51.2〜53.8dB、最大画素差127。完全一致・正解画像に対する品質評価ではない。初回約14MB取得/mesh構築は残り、区間化・品質別model・総メモリ・smartphone実機性能は未評価。顔/手の残像や撮影範囲外の品質をこの描画経路で改善したとは扱わない。
