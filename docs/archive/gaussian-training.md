# 人物シーンでの動的Gaussian学習

UVG-GS BlueSquatのローカル実験。輪郭・時刻別RGBD初期化・depth監督で人物領域PSNRは16.169→20.206 dBへ改善したが、未知視点の顔・手の崩れは残った。FreeTimeGS公式実装の再現や目標画質の達成ではない。現workerは[共同学習](../dynamic-gs-training.md)を参照。

## 素材と評価条件

[UVG-GS](https://tie-ultravideo.rd.tuni.fi/UVG-GS/index.html)の登録済み12camera、source frame 56–75/25fpsを使用。GSデータ公開は2026年だが、同sequenceはUVG-VPCでも公開済みで、撮影年を示さない。[利用条件](https://tie-ultravideo.rd.tuni.fi/UVG-GS/UVG-GS_LICENSE_AGREEMENT.pdf)は非商用研究等に限定され、入力・モデル・実験コードは公開リポジトリに含めない。

8 train、4 held-out×20frame＝80画像、960×803、黒背景、seed0、20,000 iteration、初期150,000/上限250,000 Gaussianを固定。評価RGB/depthは学習・初期化に使わず、共通の配布校正と学習cameraだけからの正規化を使用した。VRUのPSNRとは直接比較しない。

元RGB/maskを縮小してKを保持し、配布COLMAP画像との対応を確認。追加undistortは適用せず、再取得データ・派生画像・校正・初期点群のSHA256を記録した。

## 学習方法

[FreeTimeGS](https://arxiv.org/html/2506.05348v1)を参考に、位置・速度・中心時刻・持続時間を学習した。

```text
position(t) = position + velocity × (t − centre_time)
opacity(t) = sigmoid(opacity) × exp(−0.5 × ((t − centre_time) / duration)²)
```

gsplat 1.5.3、視点非依存RGB、0.8 L1＋0.2 SSIMと小さな空間scale正則化を使用。論文のSH・知覚loss・periodic relocation等は再現しておらず、論文の品質・速度を引き継がない。

| 比較段階 | 学習入力・変更 |
| --- | --- |
| RGB基準 | 学習RGBDから50,000点を作り、3中心時刻へ複製。IR depthをZ軸180°回転→NUC→提供transformでworldへ変換。別RGB校正との合成とmask整合を確認。RGB-only再構成ではない。 |
| 輪郭loss | 同じ形状・学習順でalpha対学習maskのL1係数を0→0.1。 |
| 時刻別初期化 | frame 56/65/75の学習depth・色から各50,000点、計150,000点。RGB/mask/camera/評価/順序/iteration/上限は維持。 |
| depth監督 | 同じ初期値＋全20時刻×8 trainのIR depth。RGB投影で手前の正Zを残し、mask境界2pixelを除外。[expected depth](https://docs.gsplat.studio/main/apis/rasterization.html)対正規化camera ZのL1、係数0.1。取得元・変換・SHA・train限定を監査。 |

後二段階はdepth入力の増加を含み、入力量とloss単独の効果を分離していない。

## 結果

同じ80画像のfloat描画平均。PSNRは高いほど、IoUは1に近いほどよい。

| 条件 | 全体PSNR (dB) | 人物領域PSNR (dB) | SSIM | 輪郭IoU |
| --- | ---: | ---: | ---: | ---: |
| RGB基準 | 20.979 | 16.169 | 0.8355 | 0.6070 |
| ＋輪郭loss 0.1 | 21.609 | 17.732 | 0.8536 | 0.8413 |
| ＋3時刻RGBD初期化 | 24.155 | 19.715 | 0.8883 | 0.9271 |
| ＋全20時刻depth監督 | 24.645 | 20.206 | 0.8925 | 0.9384 |

輪郭lossはIoUを80/80で改善し、人物PSNRは69/80改善・11/80低下。保存PNGの別CPU評価器でも同傾向だった。depth候補の人物PSNRは基準比72/80改善・8/80低下、時刻別初期化比59/80改善・21/80低下（平均約0.49 dB増）。顔・腕・手の残像は残り、全画像や製品品質の改善とはしない。配布校正の光学精度・RGB/depth補正工程は独立検証しておらず、VRUへの効果も未検証。

静的診断はframe 56、6,000step、初期50,000→64,546点。評価4画像で全体27.088 dB/人物19.230 dBとなり顔の崩れが残った。点数・stepが異なり原因特定の対照ではないが、時間変形だけに原因を限定できなかった。

非公開ローカルGPU（VRAM 6GB）で測定。学習秒はcheckpoint保存を含み、準備・取得・評価を除く。実行順や同一マシンの影響を統制した速度比較ではない。snapshotは量子化済み実サイズ。

| 条件 | 学習秒 | 20frame合計bytes | 初回frame bytes |
| --- | ---: | ---: | ---: |
| RGB基準 | 676.8 | 79,312,256 | 4,402,112 |
| ＋輪郭loss | 650.9 | 84,456,256 | 4,710,560 |
| ＋時刻別初期化 | 669.0 | 74,092,512 | 3,106,560 |
| ＋depth監督 | 741.1 | 74,625,408 | 3,107,712 |

最終約237,000〜250,000点、Torch peak allocated約341〜381MB。CUDA contextやブラウザを含む端末全体のメモリではない。

## 配信・表示への接続

当時は20個の`.splat` snapshotを離散選択。RGBA8/quaternion8量子化を含み可逆ではない。後続の[連続再生](../temporal-playback.md)は同じcheckpointを時間付きモデルとして配布する。

基準/depth snapshotを25fpsで全取得すると約0.79/0.75 Gbps/人の計算で、モバイル帯域は未達。CDNだけでは端末取得量を減らせず、区間化・品質別資産・端末時刻評価が必要となる。

保存`.splat`を再読込し、同カメラ・frame 0/10/19の12条件でfloatと比較。人物PSNRは基準15.809→15.805 dB、depth 19.342→19.341 dBで順位を保持した。全80画像の代替ではないが、大きな残像をpackingだけでは説明できない。

sceneの`camera.intrinsics`に縮小後width/height/fx/fy/cx/cy、`referenceImageHeight`に803を指定。仮想orbitの見え方と正解のあるcamera評価は分ける。

Windows Edge / ローカルGPU（ANGLE D3D11）、390×844/1440×900で候補・基準のAPI→動画→自由視点→frame 0/10/19→orbit→動画復帰を確認。JS/GLエラー0、離脱後Worker0。同bundleのVRU回帰でbuffer高さ803/270を維持した。スマートフォン実機の性能測定ではない。
