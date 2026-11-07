# 自由視点の連続再生

従来の`.splat`列では、時刻ごとにHTTP取得・parse・mesh構築・GPU転送が必要だった。連続再生では、位置の速度と時間方向の不透明度を持つモデルを一度読み込み、同じmeshの時間uniformと描画順だけを更新する。再生中も視点を操作でき、シークでもHTTP取得やmesh交換を行わない。

対象は[学習比較](gaussian-training.md)で使ったFTGS-inspiredモデルで、一般の4DGS形式への互換性を保証するものではない。Google Cloudなど特定の配信環境には依存せず、版付きHTTP資産として配布する。ABR制御との接続は未実施。

## シーンとバイナリ形式

scene manifestの`camera`・`attribution`は既存の契約を使い、`frames`を次の指定で置き換える。

```json
{
  "frameRate": 25,
  "frameCount": 20,
  "temporal": { "url": "model.rgs" }
}
```

`.rgs`はlittle endianのRGS1形式。URLはmanifestの最終URLを基準に解決する。入力は32MiBまで、frame数は2〜15000、rateは正の有限値で240以下とする。ヘッダーとmanifestのframe数・rateの一致、各floatの有限性、正のscale・duration、opacityの範囲を検証する。

32byteのヘッダー:

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

モデル時刻`t`は物理秒を最終時刻で割った値。位置は`mu + velocity * (t - tau)`、不透明度は`peak * exp(-0.5 * ((t - tau) / duration)^2)`とする。scale・回転・色は時間に対して一定。alphaは既存snapshotと同じ8bit・ties-to-evenで丸め、1未満を描画対象から外す。初期時刻で透明なGaussianも保持し、後の時刻で出現できるようにする。Gaussianの追加間引きは行わない。RGBとquaternionはsnapshotと同じ量子化を含むため、可逆圧縮ではない。

## 描画と資源管理

WorkerがHTTP取得・検証・初回buffer生成を行い、固定ID順のbufferとmotion textureをtransferする。SDKの空間分割でIDが変わらないよう、固定版の`preallocateUncompressed`とparserを使う。meshは一度だけ構築し、SDK標準の静的sort workerを終了する。頂点shaderへの時間評価の追加は2か所のソース一致を確認し、SDKが変わった場合は明示的に失敗させる。

再生中はWorkerが動いた中心とalphaから描画対象を選び、float32深度キーを4回のradix passで奥から手前へ並べる。同時に1件だけ処理し、index bufferはtransferして再利用する。main threadはその結果と同じ時刻・カメラの組を反映する。操作中の希望カメラは別に保持し、古いソート結果を新しいカメラへ適用しない。shaderの位置・alpha以外のテクスチャは更新しない。

再生時計はsource rateを使い、最後のframeの表示時間を含めてループする。20frame/25fpsなら0.8秒周期で、最終姿勢は0.76秒。描画速度に合わせて再生時計を遅くする方式ではない。手動seekは古い結果を無効にし、最新の要求へ集約する。通常の進捗通知は最大10Hzとし、各frameでFlutterへreadyを送り直さない。

UIは最初のGPU描画まで通常動画を残し、準備中も再生・停止を受け付ける。自由視点は再生ボタン・ダブルクリック・ダブルタップで再生と停止を切り替え、ドラッグや複数指操作をダブルタップと区別する。動画へ戻るとWorkerとGPU資源を保持したまま描画・再生時計を停止する。同じ項目で再入場すると再利用する。非表示中の経過時間は再生位置に加算しない。

保持は表示中の1項目に限る。準備のキャンセル・項目の移動・エラーでWorkerを終了し、追加textureとviewerを解放する。旧来の`.splat`列は既存のシーク経路を維持し、連続再生の操作は表示しない。

## ローカルで確認した範囲

UVG-GS BlueSquatの同じ学習済みモデルから、249,895 Gaussianを保持するRGS1をCPUで出力した。20個のsnapshot合計74,625,408byteに対しRGS1は13,994,152byte。比較用基準モデルは237,321 Gaussianで、79,312,256byteから13,290,008byteとなった。カメラ・評価時刻は変更していない。両モデルのframe 0/10/19をCPUでfloat32評価し、描画対象のsnapshot recordがbyte一致することを確認した。

Windows Edge・NVIDIA RTX PRO 500 Laptop・ANGLE D3D11、960×803・DPR 1・基準高さ803の同一マシン内HTTPで、候補モデルの連続再生を約4秒計測した。

| 指標 | 観測値 |
| --- | --- |
| 実際のinstanced draw | 60.0回/秒 |
| 時間・sort結果の反映 | 49.8回/秒 |
| draw間隔 p95 / 最大 | 17.3 / 17.5ms |
| 常駐 / 時刻別描画Gaussian | 249,895 / 約97,000〜138,000 |
| 定常時のtexture転送・texture/buffer確保 | 0回 |
| モデルHTTP取得 / `.splat`取得 | 1 / 0回 |

drawの回数と新しい時刻の反映回数は異なる。継続中はindexの`bufferSubData`更新がある。JavaScript・shader・WebGLエラーは0で、loop、orbit、seek、ダブルクリック・ダブルタップでの再生・停止を確認した。実測ログと画像はローカルに保持する。

同じWindows GPUの実アプリでも390×844と1440×900で候補・基準の2clipを確認した。モデルの応答を遅延させた状態での通常動画の再生・停止、GPU準備後の切替、連続再生中の視点操作、frame 0/10/19のシーク、動画への復帰と同一iframe・Workerの再利用、項目移動での解放が通った。各clipのモデル取得は1回、再入場時の追加取得は0回で、JavaScript・console・HTTPエラーは0だった。

同じカメラ・frame 0/10/19で旧snapshot表示と比較した画像の平均絶対差は8bit値で0.047〜0.059、PSNRは51.2〜53.8dBだった。GPUの表示は完全一致ではなく、一部画素の最大差は127だった。これは旧描画との比較であり、正解画像に対する画質評価ではない。

初回は約14MBの取得とmesh構築が残り、区間化・品質別モデル・端末での総メモリ評価は未実施。スマートフォン幅での機能確認をスマートフォン実機の性能とみなさない。新たな描画経路だけで、学習モデルに残る顔・手の残像や撮影範囲外の品質が改善するものではない。
