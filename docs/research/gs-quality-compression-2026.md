# 4DGSの画質と容量：CVPR 2026を中心にした調査

調査日: 2026-11-09。対象は下記8論文。**最初は、既存RGS1を保つ動きの補助損失と、同じモデル列の可逆な時間差分を別々に比較する。** 大きな圧縮率を示す研究の多くは、表現・学習・量子化・専用decoderを一緒に変更している。論文のPSNRや倍率をrangleのVRU/liveへ移せる根拠はまだない。本書は調査と比較案であり、実装・学習・GPU実験・設定変更は行っていない。

## 現在地と比較の分離

正本は `stg d43798d`。現在の[local live結果](../live-training.md)は、最終held-out PSNR **20.5904 dB / SSIM .674379**。VRUの34カメラ、480×270、25fps、30 train / 4 held-out、20時刻windowを使用する。約5.8MB/windowの既存RGS1はfloat欄をFP32で保持し、既存RGB/alpha/quaternion packingと可逆Z1圧縮を含む。20フレームの窓刻み0.8秒ごとに全windowを送れば約7.25MB/s、58Mb/sになるという**概算**であり、dropや実際の更新間隔を含む配信実測ではない。RGS1の時間幅自体は19/25＝0.76秒である。

画像の最適化は到着済み時刻のみだが、著者供給seed/calibrationはheld-outカメラや初期時刻の情報を含み得る。window移動時には寿命位相を局所時刻へ戻す近似があり、残像・静止背景の漂い・境界の不連続が残る。CPU動画decodeと、毎回のfull model転送も別の費用である。cloudのproof/CPU shape比較の結果は本書へ含めていない。最適化window数・総stepが違うrunのPSNR差を、ML手法の改善と扱わない。

| 比較する層 | 判定する内容 | 混ぜない内容 |
| --- | --- | --- |
| 学習・画質 | 動き/寿命/可視性の誤り、同じheld-outでの時間整合性 | 異なる解像度・seed・step数で得たPSNR差 |
| 表現圧縮 | 量子化、VQ、pruning、学習したlatentのrate–distortion | 元floatへのbit-exact復元という意味の「可逆」 |
| 転送・復号 | full model再送の回避、圧縮bytes、復元CPU/RAM、途中参加 | 描画FPS、学習時間、データ到着の遅れ |

既存の[同一モデルの可逆圧縮](../temporal-compression-comparison.md)は12,106,161→11,967,872B、1.142%減である。[学習profileによる容量差](../training-quality.md)にはGaussian数の変化も入る。既存`motion-prior`は12k-step比較で平均画質を下げたため、その速度ペナルティを改善済みとして再提案しない。

## 8論文の根拠と測定条件

会議・公開月はCVF公式proceedings、手法・数値は下記の本文/著者公開版を参照した。2026年のarXiv投稿日を会議年と取り違えない。特にSeeLeは2025年arXiv版を持つ**CVPR 2026**論文、4DGC/GIFStreamは**CVPR 2025**である。未確認は「論文に存在しない」ではなく、今回参照した一次資料から確認できなかった項目を表す。

| 論文・掲載月 | 対象 / 品質の仕組み / 入力の扱い | 圧縮形式・倍率の分母 | 学習条件・時間・peak VRAM | 復号・描画で確認できたこと |
| --- | --- | --- | --- | --- |
| **SharpTimeGS** — [CVPR 2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Liao_SharpTimeGS_Sharp_and_Stable_Dynamic_Gaussian_Splatting_via_Lifespan_Modulation_CVPR_2026_paper.html)、[本文v1](https://arxiv.org/html/2602.02989v1) | 4D、offline。flat-top寿命gate、寿命に応じた速度抑制、動き/寿命を使う再配置。RAFT/SAM2と時刻別点群から初期化し、現在の単一seed条件とは異なる。 | 主眼は表現効率。今回、codecとしての圧縮率は未確認。点数減とbit圧縮を同一視しない。 | Neural3DV最初300fを半解像度、ENeRF-Outdoor 300f、SelfCap 60f。学習秒・peak VRAM未確認。4090は描画条件。 | 著者報告はRTX 4090で4K/100FPS。著者pageにiPad例があるが、端末仕様・復号時間・RAMの定量値は未確認。 |
| **MoRGS** — [CVPR 2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Lee_MoRGS_Efficient_Per-Gaussian_Motion_Reasoning_for_Streamable_Dynamic_3D_Scenes_CVPR_2026_paper.html)、[本文v1 §5](https://arxiv.org/html/2603.25042v1) | 4D、frame単位online。疎なflow＋motion offset＋confidenceで、画素残差だけを追う動きを抑える。初期frameを別途長く学習する。 | N3DV Table 1: MoRGS-l **3.80MB/frame、32.53dB**、QUEEN-l **.75MB/frame、32.19dB**。画質重視で、最小容量ではない。初期frameを含む300f平均。 | N3DV 20 views / Meet Room 13 views、300f@30fps、中央1view評価、RTX A5000。初期10k/15k iter＋各後続frame 8 epochs/view。MoRGS-l平均4.0s/frame。peak VRAM未確認。 | 200FPSはA5000の描画値。SEA-RAFTは4 key views、keyframe間隔5、SAM2も使用。flow/segmentationのcold startupとmobile CPU負担は未確認。 |
| **ClipGStream** — [CVPR 2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Liang_ClipGStream_Clip-Stream_Gaussian_Splatting_for_Any_Length_and_Any_Motion_CVPR_2026_paper.html)、[本文v1 Tables 2–3](https://arxiv.org/html/2604.13746v1) | 4D、clip内共同最適化＋clip間anchor/decoder継承。境界flickerと大きな動きを扱う。clipが揃ってから学習するため、frame即時処理とは異なる。 | N3DV 300fの表: **98MB/sequence**、LocalDyGS 100MB。専用codecの大倍率ではない。VRU GZの著者値30.67dBは現行480×270の評価値と比較不可。 | N3DV 5 scenes/300fで0.5h、VRU GZ 34cam/250f/25fpsも評価。VRUは1920×1080原画像の設定を記載。学習GPU型・peak VRAMは今回未確認。 | N3DVの106FPSはMLP/anchorを使う専用描画。browser/mobile decode時間は未確認。 |
| **L4DRotorGS** — [CVPR 2026/06、本文§3.4・Table 1](https://openaccess.thecvf.com/content/CVPR2026/papers/Xu_Layered_4D-Rotor_Gaussian_Splatting_A_Compressed_Representation_for_Long_Dynamic_CVPR_2026_paper.pdf) | 4D、offline。時間layer/bucket、CPU/GPU triple buffer、長寿命点のtemporal-rotor学習率抑制。本文がonline training非対応と明記。 | SQ/VQ/残差codebook。N3DVの同一手法: **180.7→8.8MB、20.5×**、PSNR **32.23→31.84dB**。別baselineとの倍率ではない。 | N3DV 6 scenes、1352×1014、10s。RTX 3090、学習約30min、peak **2GB**、圧縮約4min。長いSelfCapの時間/GPUは別条件。 | C++/CUDAでactive bucketを復号・常駐。Smallの約661FPSは3090。browser/WASM/携帯端末は未確認。 |
| **PackUV** — [CVPR 2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Rai_PackUV_Packed_Gaussian_UV_Maps_for_4D_Volumetric_Video_CVPR_2026_paper.html)、[本文v1 §4.2.4・6・11–13](https://arxiv.org/html/2602.23040v1) | 4D、順次fitting＋keyframe/flow-based Gaussian labeling/静的領域の更新制限。表現をUV上で直接最適化。全sequence min/maxの符号化設定はcausal liveへそのまま使えない。 | 位置16bit、他属性8bitのLPO後atlasをFFV1等で可逆符号化。**codecの0誤差はatlas基準**。No-LPOとの画質差は27.52→27.41dB。現FP32 RGS1に対する圧縮率は未測定。 | N3DV/SelfCap/PackUV-2Bを1.6Kへ縮小。3090、RAM256GB、8 CPU cores。60時刻あたり1.05–1.37h（Table 2の各dataset）。peak VRAM未確認。 | 専用OpenGL/SSBO viewer、CPU buffer→GPU。3090でI/O除外>200FPS、I/O込み≥30FPS。これはWebCodecs/mobileの実測ではない。 |
| **4DGC** — [CVPR 2025/06](https://openaccess.thecvf.com/content/CVPR2025/html/Hu_4DGC_Rate-Aware_4D_Gaussian_Compression_for_Efficient_Streamable_Free-Viewpoint_Video_CVPR_2025_paper.html)、[本文v1 §4・Tables 1/4/6](https://arxiv.org/html/2503.18421v1) | 4D、初期3DGS＋後続frameのmotion grid/補償Gaussian。rate–distortion loss、微分可能量子化、entropy modelを共同学習。 | N3DV 300f平均: **3DGStream 8.1→4DGC .5MB/frame ≈16×**、31.54→31.58dB。別表のkeyframe7.3–17.3MBとinter .23–1.21MBを平均値から隠さない。 | Xeon W-2245＋RTX 3090、後続frame 400 motion＋100 compensation iter。Table 4: train .83min、encode .72s、decode .09s。初期学習の分離/cold startとpeak VRAMは未確認。 | motion-grid/entropy decoderが必要。168FPSの描画と90msの復号は別。T4/CPU/ブラウザ値は未確認。 |
| **GIFStream** — [CVPR 2025/06](https://openaccess.thecvf.com/content/CVPR2025/html/Li_GIFStream_4D_Gaussian-based_Immersive_Video_with_Feature_Stream_CVPR_2025_paper.html)、[本文v1 §4](https://arxiv.org/html/2505.07539v1) | 4D、GOP共同学習。canonical＋deformation feature stream、動的領域に重きを置くdensification、QAT＋entropy loss。全GOP分の待ち時間がある。 | Bartender ablation: **同手法非圧縮46.1→5.3MB、32.13→31.94dB**。30Mb/sは別の総括値で、倍率とは混ぜない。rANSまたはvideo codec。 | Neur3D/Panoptic Sportsの2scene/MPEG、GOP 30k iter。MPEGは65f・原1080p。総学習時間・peak VRAM未確認。 | 4090で描画>60FPS、Bartender約2s分のdecode<1s。学習NN/entropy復号を伴い、browser/mobile値は未確認。 |
| **SeeLe** — [CVPR 2026/06](https://openaccess.thecvf.com/content/CVPR2026/html/Zhu_Seele_A_Unified_Acceleration_Framework_for_Real-Time_Gaussian_Splatting_on_CVPR_2026_paper.html)、[著者版2025 v1](https://arxiv.org/html/2503.05168v1) | **静的3D**、offline cluster/fine-tune＋runtime view選別/寄与選別。4Dの残像を学習で直す手法ではない。 | CVF要旨はmodel **39.1%減**、v1はGaussian常駐weight32.3%減、主repoは32.5%と版差がある。ファイル/通信bytesの率へ換算せず、版も混ぜない。 | Mip-NeRF360/Tanks & Temples/Deep Blending。学習/fine-tune総時間・peak学習VRAM未確認。 | AGX Orin、Orin NX、A6000を評価。v1 Table 2はscene/手法でFPSが大きく異なる。CUDAのmobile SoCを、スマートフォンWebGLの実証とは扱わない。 |

各行の数値は著者報告で、rangle上の再現値ではない。単位は論文どおりのMBを残した。per-frame平均、sequence全体、常駐VRAMを横並びの「圧縮率」にしない。ClipGStreamとMoRGSは異なるscene集合・分割・初期学習を持ち、似たPSNR同士でも優劣を計算しない。GIFStreamの非圧縮との比較も、同じ重みを可逆再encodeした比較ではなく、圧縮学習有無のablationである。

## 実装・licenseと現RGS1への距離

2026-11-09時点の著者page、repo root/README、公開LICENSEを確認した。root LICENSE未確認は、利用条件が自由という意味ではない。論文自体の公開licenseとコード/依存/weights/datasetのlicenseは別である。

| 論文 | 著者実装とlicenseの確認結果 | 現gsplat / RGS1への接続（本調査の判断） |
| --- | --- | --- |
| SharpTimeGS | [著者page](https://liaozhanfeng.github.io/SharpTimeGS/)から実装・code licenseを確認できず。 | 寿命radius追加とflat-top opacityは現JS/RGS1式と異なり、完全移植には新wire/shaderが要る。動き/寿命の補助損失という一部の着想は別途比較できる。 |
| MoRGS | [公式repo](https://github.com/LeeWonJoon9868/MoRGS)にtrain code、RAFT/SAM2とCUDA submodules。root LICENSE未確認。 | flow補助だけなら学習時に追加し既存wireを保てる。論文全体のmotion offset/confidence/依存weightsまで移す費用は別。初期10k–15kを300step startupへ読み替えない。 |
| ClipGStream | [公式repo](https://github.com/liangjie1999/ClipGStream)にReference/Source Clip trainer、tiny-cuda-nn等。root LICENSE未確認。 | anchor/MLP decoderを持ち込むと現RGB＋linear velocity viewerを置換する。まず「学習状態をclip間で継承する」思想との違いを確認する段階。 |
| L4DRotorGS | [著者page](https://m1sak1-mei.github.io/layered-4d-rotor/)でC++/CUDAを説明するが、配布code/license linkは確認できず。 | 条件付き4D Gaussianと現式は近縁だが、SH・rotor codebook・bucket復号が必要。全primitiveをRGS1へ展開すれば常駐/通信上の利益を失い得る。 |
| PackUV | [著者のcode参照先](https://github.com/aashishrai3799/packuv)はREADMEのみ。[brown-ivl repo](https://github.com/brown-ivl/packuv)はwebsite資産。実装/license未確認。 | 低精度UV fitting・atlas・codec・GPU転送を新設する大きな変更。既存floatの単純なformat変換ではない。 |
| 4DGC | [公式repo](https://github.com/qianghu-huber/4DGC)に学習/codec実装、root LICENSE未確認。 | motion grid/補償点/entropy decoderとRD学習が必要。通常のRGS1 snapshotへ展開すると、時間間のcompact representationを失う。 |
| GIFStream | [公式repo](https://github.com/XDimLab/GIFStream)、[LICENSE](https://github.com/XDimLab/GIFStream/blob/main/LICENSE)はファイル別の混合条件。独自/gsplat側Apache-2.0、Scaffold-GS由来部分はnon-commercial research条件。 | gsplatベースである点は近いが、feature/deformation/NN entropy復号はRGS1 readerにない。repo全体をApacheと見なして取り込めない。 |
| SeeLe | [公式repo](https://github.com/SJTU-MVCLab/SeeLe)、[root LICENSE.mdはMIT](https://github.com/SJTU-MVCLab/SeeLe/blob/main/LICENSE.md)。submodulesは別確認が必要。 | CUDA rasterizer改変・cluster prefetchはWebGLへ直結しない。view選別は配信/VRAMの将来案で、今の可逆byte圧縮や動的品質改善とは分ける。 |

## PackUVの「lossless」を読む際の境界

[本文§4.2.4/12.4](https://arxiv.org/html/2602.23040v1)は、位置を16bit、scale/rotation/opacity/colorを8bitとして最適化し、位置を2つの8bit channelへ分割する。§6.2/11のFFV1による0誤差はそのatlasを復元する段階の主張であり、**入力FP32 RGS1の全bit一致を示していない**。同論文のNo-LPOとLPOのPSNR差も残っている。全sequenceのmin/maxを使う正規化は、liveでは固定既知範囲か到着済み範囲へ作り直す必要がある。

HEVCにも可逆/非可逆の設定差があり、codec名だけでは無劣化と判断できない。さらに8bitの属性画像を通常の映像色変換・chroma subsampling・非可逆設定へ通す場合、属性bytesの保存検証が必要になる。

[W3C WebCodecs registry（2026-09-24 draft）](https://www.w3.org/TR/2026/DRY-webcodecs-codec-registry-20260924/)のvideo一覧にFFV1はなく、HEVCの登録はあっても実装の対応は必須ではない。[`VideoDecoder.isConfigSupported`](https://developer.mozilla.org/en-US/docs/Web/API/VideoDecoder/isConfigSupported_static)は対象端末・具体的profile/bit depthで確認する。FFmpegで復号できることから、iPhone/Android browserのnative hardware復号を推定しない。FFV1をWASMへ持ち込む場合のCPU時間、buffer複製、peak RAM、電力は**未測定**であり、現RGS1 readerの代替として速い根拠はない。

## 最初の2比較案（未実行）

以下は次の実装・検証を選ぶための事前案である。今回の調査からGPU実験の許可を広げない。sourceは同じ承認済みVRU bundle、元動画・calibration/seed・480×270・25fps・30 train/4 held-out `[0,10,20,30]`・seed 0・初期99,330点・window20を固定する。GPU/CPU shape、driver、image digestも両条件で固定し、CPU shapeの比較を同時に混ぜない。

### 1. 既存wireを保った、疎なflow補助の最小ablation

MoRGSから「photometric lossだけでは動きが画素残差を追う」という問題設定を取り出す。現`local-lifetime`と比較し、寿命式・warm shift・densification・初期化・optimizer/LR・bootstrap300/update300・decode workers4を固定したまま、**training view `[1,11]`の到着済み隣接frameによるflow項だけ**を追加する案とする。既存`motion-prior`の単なる静的速度罰則とは異なり、観測された動きの向きと大きさも拘束する。

最小比較では、既存OpenCV 4.13の[DIS PRESET_MEDIUM](https://docs.opencv.org/4.10.0/de/d4f/classcv_1_1DISOpticalFlow.html)・CPU 1 threadを固定する案とする。forward/backward整合1px以内のsampleに限り、projected Gaussian displacementとflowのHuber誤差（δ=1px、幅480で正規化）を重み0.01で加える、という**rangle向け仮説**を事前固定する。これはMoRGS完全再現ではない。held-outからflow/mask/閾値を作らず、window外の将来frameを参照しない。補助flowのcode/パラメータ確定は実装前に別途レビューする。

品質比較は、同じsource PTS/window列を同じ順序・step数で両条件に渡す。producerの速度差で学習windowまで変わる比較は混ぜず、ライブでのdrop/遅延は別に測る。最初は同一10秒traceでbaseline/候補各1回、追加探索なし。各windowの同じ80 held-out画像でfloat/packed PSNR・SSIM、`mean |(I[t+1]-I[t])-(GT[t+1]-GT[t])|`、境界直前/直後の誤差を報告する。前後render差が大きいだけでは動きが正しい証拠にしない。flow生成時間・学習時間・VRAM・点数・bytesも記録し、画質と費用を両方見て次を判断する。

SharpTimeGSのflat-top寿命や、PackUVの静的領域の完全freezeは、このablationへ同時投入しない。切り分け後も寿命resetの不連続が支配的なら、次段階で表現/寿命を検討する。

### 2. 同じRGS1列で、可逆なkeyframe＋byte差分を比較

追加学習をせず、既存baselineの**同一の固定model列**を入力にする。現full snapshot再送と、過去モデルを参照するXOR差分＋既存byte shuffle/RLEの1候補を比較する。静的属性の同一byteは再利用できるが、近いfloatが同じbyteとは限らないため削減率は未知である。これは論文codecの移植ではなく、4DGC/PackUVが示す時間相関の利用を、既存wireのまま試す案である。

canonical record順・全float bit・不可視primitiveを保持する。行の変更は圧縮効率を下げ得るが、差分を正しく戻せれば同一性は保てる。5windowごとを独立keyframe、差分がfullより大きい/対応できない場合もfullへfallback、参照chainは最大4差分で固定する。count変化時は独立keyframeへ戻す。元RGS1 rawの全byte/SHAを照合し、既存model SHA契約を保つ場合はpinしたencoderで再構成した`model.rgs`のSHAも一致させる。不一致を「見た目が同じ」で通さない。

評価する量は、keyframe・codebook/参照metadata・fallback・再取得を含む**総bytes/ source秒**と**総bytes/実送信秒**、encoder/decoderのCPU時間、初回表示まで、途中参加/seek時の必要bytesと待ち時間、peak CPU/GPU memory、破損/参照欠落時の復旧である。Android/iPhoneの対象browserでcold/warm各3回の中央値・最大を測り、少数試行からp95を保証しない。全byte一致なら同じ時刻・readerへ渡すモデル値は同一で、新しい量子化誤差を持ち込まない。差分chainが通信量を減らしてもstartup/RAMを悪化させる可能性を残す。既存full RGS1へのfallbackとversionを持つ配信契約の検討は、結果が出た後の別作業とする。

## 非可逆方式へ進む際の判断点

FP16、8/16bit atlas、VQ、Gaussian削減は**比較候補まで**で、今回採用しない。既存の[FP16比較](../temporal-compression-comparison.md)でも、平均PSNRが近い候補に最大RGB差約0.24があった。将来の候補は同じheld-out全画像・全時刻で、平均値に加えて局所max差、時間誤差、最初/中間/最後とwindow境界の比較画像、端末decode/peak RAMを出す。許容誤差が未決定のまま配信へ採用せず、具体的な差を提示して承認を得る。それまでは既存原wireだけを配信する。

GPUの描画FPSが高くても、元動画decode・初期学習・flow/NN推論・圧縮・転送・client startupを含む約30秒のlive目標を満たすとは限らない。研究の再現を先に大きく作るより、上記2比較で画質の支配要因とbit-exactの削減余地を分けて確かめるのが、現在の条件に合う。
