# 4DGSの画質と容量：CVPR 2026を中心にした調査

2026-11-09調査、8論文の根拠と比較案。**既存RGS1を保つ動き補助lossと、同一モデル列の可逆時間差分を別々に比較する案**を示す。実装・学習・GPU実験・設定変更は未実行。論文の圧縮率/PSNRをrangleのVRU/liveへ転用できる根拠はない。

## 現在地と比較の分離

調査時点は`stg d43798d`。[local live](../live-training.md)はVRU34camera、480×270、25fps、30 train/4 held-out、20時刻windowで最終20.5904 dB/.674379。RGS1はFP32欄＋既存RGB/alpha/quaternion packing＋可逆Z1。約5.8MBを0.8秒刻みで全送信すると約7.25MB/s＝58Mb/sの**概算**だが、実際のdrop/更新間隔を含まない。モデル時間幅は19/25＝0.76秒。

画像は到着済みだけを最適化するが、供給seed/calibrationはheld-outや初期時刻の情報を含み得る。寿命を局所時刻へ戻す近似に残像・背景漂い・境界不連続が残り、動画decode/full model転送も費用となる。cloud proof/CPU shape比較は本書に含めず、window数/総stepの違うrunをML改善としない。

| 層 | 比較対象 / 混同しないもの |
| --- | --- |
| 学習・画質 | 同一held-outで動き/寿命/可視性/時間整合性を比較。解像度/seed/step差は分離。 |
| 表現圧縮 | 量子化/VQ/pruning/latentのrate–distortion。元floatへのbit-exact復元とは区別。 |
| 転送・復号 | 再送bytes/CPU/RAM/途中参加。描画FPS/学習時間/入力遅延とは分離。 |

[同一モデル圧縮](../temporal-compression-comparison.md)は12,106,161→11,967,872B（1.142%減）。[学習profile差](../training-quality.md)は点数変更も含み、12k-stepで平均品質を下げた`motion-prior`を改善済みとして再提案しない。

## 8論文の根拠と測定条件

会議/月はCVF、手法/数値は本文・著者版による。SeeLeは2025年arXiv版を持つCVPR2026、4DGC/GIFStreamはCVPR2025。「未確認」は参照資料から確認できなかった意味。以下はすべて**著者報告、rangle再現値ではない**。

| 論文・出典 | 対象・品質手法 | 圧縮形式と分母 | 学習条件 / 復号・描画 |
| --- | --- | --- | --- |
| **SharpTimeGS** [CVPR2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Liao_SharpTimeGS_Sharp_and_Stable_Dynamic_Gaussian_Splatting_via_Lifespan_Modulation_CVPR_2026_paper.html)・[本文v1](https://arxiv.org/html/2602.02989v1) | offline4D。flat-top寿命、寿命別速度抑制、動き/寿命再配置。RAFT/SAM2・時刻別点群初期化は現単一seedと異なる。 | 表現効率が主眼。codec倍率未確認、点数減とbit圧縮は別。 | Neural3DV最初300f/半解像度、ENeRF-Outdoor300f、SelfCap60f。学習秒/peak VRAM未確認。RTX4090描画4K/100FPS、iPad例は仕様/復号/RAM未確認。 |
| **MoRGS** [CVPR2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Lee_MoRGS_Efficient_Per-Gaussian_Motion_Reasoning_for_Streamable_Dynamic_3D_Scenes_CVPR_2026_paper.html)・[v1 §5](https://arxiv.org/html/2603.25042v1) | frame-online4D。疎flow/motion offset/confidenceで画素残差追従を抑制。初期frameは別途長期学習。 | N3DV Table1、初期込み300f平均：MoRGS-l **3.80MB/frame・32.53dB**、QUEEN-l **.75MB/frame・32.19dB**。最小容量ではない。 | N3DV20/Meet Room13views、300f@30fps、中央1view評価、A5000。初期10k/15k iter＋後続8epochs/view、平均4.0s/frame、描画200FPS。SEA-RAFT4 key views/間隔5＋SAM2。peak VRAM/cold start/mobile CPU未確認。 |
| **ClipGStream** [CVPR2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Liang_ClipGStream_Clip-Stream_Gaussian_Splatting_for_Any_Length_and_Any_Motion_CVPR_2026_paper.html)・[v1 Tables2–3](https://arxiv.org/html/2604.13746v1) | 4D、clip共同最適化＋anchor/decoder継承で境界flicker/大きな動きに対応。clip完成待ち。 | N3DV300f：**98MB/sequence**、LocalDyGS100MB。VRU GZ30.67dBは現480×270と比較不可。 | N3DV5scenes/300f、0.5h。VRU34cam/250f/25fps、原1920×1080設定。学習GPU/VRAM未確認。MLP/anchor専用描画106FPS、browser/mobile復号未確認。 |
| **L4DRotorGS** [CVPR2026/06、§3.4/Table1](https://openaccess.thecvf.com/content/CVPR2026/papers/Xu_Layered_4D-Rotor_Gaussian_Splatting_A_Compressed_Representation_for_Long_Dynamic_CVPR_2026_paper.pdf) | offline4D、時間layer/bucket、CPU/GPU triple buffer、長寿命点のrotor学習率抑制。online学習非対応。 | SQ/VQ/残差codebook。同手法N3DV **180.7→8.8MB（20.5×）**、**32.23→31.84dB**。 | N3DV6scenes/1352×1014/10s、3090、学習約30min/peak2GB/圧縮約4min。長尺SelfCapは別条件。C++/CUDA active bucket復号、Small約661FPS。browser/WASM/mobile未確認。 |
| **PackUV** [CVPR2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Rai_PackUV_Packed_Gaussian_UV_Maps_for_4D_Volumetric_Video_CVPR_2026_paper.html)・[v1 §4.2.4/6/11–13](https://arxiv.org/html/2602.23040v1) | 順次4D fitting、keyframe/flow Gaussian labeling、静的更新制限、UV直接最適化。全sequence min/maxはそのままcausal liveに使えない。 | 位置16bit/他8bitのLPO atlasをFFV1等で可逆符号化。**0誤差はatlas基準**。No-LPO→LPO **27.52→27.41dB**。現FP32倍率未測定。 | N3DV/SelfCap/PackUV-2B、1.6K、3090/RAM256GB/8CPU cores、60時刻1.05–1.37h（Table2）、peak VRAM未確認。専用OpenGL/SSBO、CPU→GPU。I/O除外>200/込み≥30FPSはWebCodecs/mobile値ではない。 |
| **4DGC** [CVPR2025/06](https://openaccess.thecvf.com/content/CVPR2025/html/Hu_4DGC_Rate-Aware_4D_Gaussian_Compression_for_Efficient_Streamable_Free-Viewpoint_Video_CVPR_2025_paper.html)・[v1 §4/Tables1/4/6](https://arxiv.org/html/2503.18421v1) | 初期3DGS＋後続motion grid/補償点。RD loss/微分可能量子化/entropy共同学習。 | N3DV300f平均 **3DGStream8.1→4DGC.5MB/frame（≈16×）**、31.54→31.58dB。別表key7.3–17.3/inter.23–1.21MB。 | Xeon W-2245/3090、後続400motion＋100compensation iter。Table4 train.83min/encode.72s/decode.09s。初期学習/cold start/VRAM未確認。専用motion-grid/entropy復号、168FPSと90ms復号は別。T4/CPU/browser未確認。 |
| **GIFStream** [CVPR2025/06](https://openaccess.thecvf.com/content/CVPR2025/html/Li_GIFStream_4D_Gaussian-based_Immersive_Video_with_Feature_Stream_CVPR_2025_paper.html)・[v1 §4](https://arxiv.org/html/2505.07539v1) | 4D GOP共同学習、canonical/deformation feature stream、動的densification、QAT/entropy。GOP待ちが必要。 | Bartender圧縮学習ablation：同手法非圧縮 **46.1→5.3MB、32.13→31.94dB**。別総括30Mb/sとは分離。rANS/video codec。 | Neur3D/Panoptic Sports2scenes/MPEG、GOP30k iter、MPEG65f/原1080p。総学習時間/VRAM未確認。4090描画>60FPS、Bartender約2sを<1sで復号。学習NN/entropy復号、browser/mobile未確認。 |
| **SeeLe** [CVPR2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Zhu_Seele_A_Unified_Acceleration_Framework_for_Real-Time_Gaussian_Splatting_on_CVPR_2026_paper.html)・[著者2025 v1](https://arxiv.org/html/2503.05168v1) | **静的3D**、offline cluster/fine-tune、runtime view/寄与選別。動的残像を直す学習ではない。 | CVF model39.1%減、v1常駐weight32.3%減、repo32.5%と版差。通信/ファイル率へ換算せず版も混ぜない。 | Mip-NeRF360/Tanks & Temples/Deep Blending、学習/fine-tune時間・peak学習VRAM未確認。AGX Orin/Orin NX/A6000、v1 Table2のFPSはscene/手法で異なる。CUDA mobile SoCはスマートフォンWebGLの実証ではない。 |

MBは論文表記。frame平均/sequence全体/常駐VRAMを同じ倍率にしない。ClipGStream/MoRGSもscene/split/初期学習が異なる。GIFStreamは圧縮学習ablationで、同一重みの可逆再encodeではない。

## 実装・licenseと現RGS1への距離

2026-11-09確認。論文・コード・依存・weights・datasetのlicenseは別。root LICENSE未確認を自由利用と解釈しない。

| 論文 | 著者実装・license | 現gsplat/RGS1との距離（本調査の判断） |
| --- | --- | --- |
| SharpTimeGS | [著者page](https://liaozhanfeng.github.io/SharpTimeGS/)、実装/license未確認。 | 寿命radius/flat-top式は新wire/shaderが必要。動き/寿命の補助lossだけなら別比較可能。 |
| MoRGS | [repo](https://github.com/LeeWonJoon9868/MoRGS)、train/RAFT/SAM2/CUDAあり、root LICENSE未確認。 | flow補助は既存wireを保てるが、全motion offset/confidence/weights導入は別費用。初期10k–15kを300stepに読み替えない。 |
| ClipGStream | [repo](https://github.com/liangjie1999/ClipGStream)、Reference/Source Clip trainer/tiny-cuda-nn、root LICENSE未確認。 | anchor/MLPは現RGB/linear velocity viewerを置換。まずclip間状態継承との差を確認する段階。 |
| L4DRotorGS | [著者page](https://m1sak1-mei.github.io/layered-4d-rotor/)、C++/CUDA説明、code/license link未確認。 | SH/rotor codebook/bucket復号が必要。全点をRGS1展開すると通信/常駐利益を失い得る。 |
| PackUV | [code参照先](https://github.com/aashishrai3799/packuv)はREADMEのみ、[brown-ivl](https://github.com/brown-ivl/packuv)はwebsite資産。実装/license未確認。 | 低精度UV fitting/atlas/codec/GPU転送を新設。既存floatの形式変換ではない。 |
| 4DGC | [repo](https://github.com/qianghu-huber/4DGC)、学習/codecあり、root LICENSE未確認。 | motion grid/補償点/entropy復号/RD学習が必要。RGS1展開は時間相関表現を失う。 |
| GIFStream | [repo](https://github.com/XDimLab/GIFStream)・[LICENSE](https://github.com/XDimLab/GIFStream/blob/main/LICENSE)：独自/gsplat側Apache-2.0、Scaffold-GS由来はnon-commercial research。 | feature/deformation/NN entropy復号が現readerにない。全repoをApache扱いできない。 |
| SeeLe | [repo](https://github.com/SJTU-MVCLab/SeeLe)・[MIT root](https://github.com/SJTU-MVCLab/SeeLe/blob/main/LICENSE.md)、submodules別確認。 | CUDA rasterizer/cluster prefetchはWebGLへ直結せず、view選別は将来の配信/VRAM案。 |

## PackUVの「lossless」を読む際の境界

[§4.2.4/12.4](https://arxiv.org/html/2602.23040v1)は位置16bit（2×8bit channel）、他属性8bitで最適化する。FFV1の0誤差（§6.2/11）はatlas復元に対し、**入力FP32 RGS1の全bit一致ではない**。No-LPO/LPOの品質差も残る。全sequence min/maxはlive向けに既知固定範囲か到着済み範囲へ変更が必要。

HEVCは設定次第で可逆/非可逆。色変換/chroma subsampling等を使う属性動画もbyte保存の検証が要る。[WebCodecs registry（2026-09-24 draft）](https://www.w3.org/TR/2026/DRY-webcodecs-codec-registry-20260924/)はFFV1未登録で、HEVC対応も必須ではない。[isConfigSupported](https://developer.mozilla.org/en-US/docs/Web/API/VideoDecoder/isConfigSupported_static)を端末/profile/bit depthごとに確認する。FFmpeg対応からbrowserのnative復号を推定しない。FFV1/WASMのCPU・複製・peak RAM・電力は未測定。

## 最初の2比較案（未実行）

事前案でありGPU実験の許可ではない。承認済みVRU動画/calibration/seed、480×270/25fps、30 train/4 held-out `[0,10,20,30]`、seed0/初期99,330点/window20、GPU/CPU shape・driver・image digestを固定する。

### 1. 既存wireを保った、疎なflow補助の最小ablation

`local-lifetime`対、train views `[1,11]`の到着済み隣接frame flow補助を比較する案。寿命/warm shift/densification/初期化/optimizer/LR/bootstrap300/update300/decode workers4は固定。既存motion-priorの速度罰則に加え、観測方向/量を拘束する仮説で、MoRGS再現ではない。

既存OpenCV4.13 [DIS PRESET_MEDIUM](https://docs.opencv.org/4.13.0/de/d4f/classcv_1_1DISOpticalFlow.html)、CPU1 thread、forward/backward誤差≤1pxのsampleを使う。投影変位対flowのHuber（δ1px、幅480で正規化）、重み0.01を事前案とし、code/値は実装前レビュー。held-out由来flow/mask/閾値や未来frameは使わない。

同じ10秒trace/window列/順序/stepで各1回、追加探索なし。producer差によるwindow変更とlive遅延/dropは別評価。各window同80画像のfloat/packed PSNR/SSIM、`mean |(I[t+1]-I[t])-(GT[t+1]-GT[t])|`、境界誤差、flow/学習時間、VRAM/点数/bytesを報告する。render差だけを動きの正しさとしない。flat-top寿命/静的freezeは同時投入せず、寿命resetが支配的なら次段階で検討する。

### 2. 同じRGS1列で、可逆なkeyframe＋byte差分を比較

追加学習せず、固定baselineモデル列でfull再送とXOR＋既存shuffle/RLEの1候補を比較する案。近いfloatも同byteとは限らず、削減率は未知。論文codec移植ではない。

全record順/float bit/不可視点を保持。5windowごと独立keyframe、chain最大4差分、count変化・非対応・fullより増量ならfullへ戻す。raw全byte/SHAを照合し、既存SHA契約を保つ場合はpin encoderの再構成`model.rgs` SHAも一致させる。行変更は圧縮効率を落とし得る。

keyframe/参照metadata・codebook/fallback/再取得込みの総bytes/source秒と総bytes/実送信秒、encode/decode CPU、初回/途中参加/seekのbytes・待ち、peak CPU/GPU memory、破損/参照欠落復旧を測る案。Android/iPhone browserは将来の許可された手元測定（GUI自動操作なし）、cold/warm各3回の中央値/最大としp95を保証しない。bit一致なら量子化誤差は増えないがstartup/RAM悪化はあり得る。version/full fallback配信契約は結果後の別作業。

## 非可逆方式へ進む際の判断点

FP16/8–16bit atlas/VQ/点削減は未採用。[FP16比較](../temporal-compression-comparison.md)も平均が近い候補に最大RGB差約0.24があった。同一held-out全時刻で平均/局所max/時間誤差、最初・中間・最後・境界画像、端末decode/peak RAMを提示し、許容誤差と採用承認が決まるまでは原wireのみ配信する。

描画FPSだけでは動画decode/初期学習/flow・NN/圧縮/転送/startup込みの約30秒目標を示せない。まず上記2比較で画質要因とbit-exact削減余地を切り分ける。
