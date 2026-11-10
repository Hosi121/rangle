# rangle 技術選定案

2026-11-07の初期判断記録。現在の採用構成は[クラウド構成](../cloud-architecture.md)、学習は[4DGS](../dynamic-gs-training.md)、接続状況は[現状](../backend-status.md)を参照。

## 初期構成の提案

| 領域 | 候補・採用条件 |
| --- | --- |
| データ・再構成 | VRU `gz`＋LocalDyGS。20フレームで前処理・再現性を確認 |
| ML | Python/CUDA。公開実装と対応づけて品質評価 |
| UI・描画 | Flutter Web＋GaussianSplats3D。native方式は実機で選定 |
| 制御・GPU | Go/Cloud Run＋有限のVertex CustomJob。進行管理はWorkflowsを候補 |
| 保存・配信 | Firestoreに状態、Cloud Storage/CDNに版付き資産 |
| 品質選択 | Throughput＋bufferのABRに復号・メモリ予算を加える |

最大の判断点は、学習出力を時間変化・視点依存性を保って端末向けに変換できるか。研究用checkpointをそのまま再生可能とは仮定しない。

## データセットの比較

| 候補 | 条件・初期判断 |
| --- | --- |
| [VRU Basketball](https://huggingface.co/datasets/BestWJH/VRU_Basketball/blob/main/README.md) | 実写3シーン・36視点。gz/dgは1080p・250フレーム・約10秒。CC BY 4.0。gzを優先 |
| [Panoptic Sports](https://github.com/JonathonLuiten/Dynamic3DGaussians) | 学習データ・事前学習モデルを公開。データ自体の利用条件を確認して比較 |
| [DNA Rendering](https://dna-rendering.github.io/inner-download.html) | 申請と署名済み同意書が必要。初期日程の前提にしない |
| [BASKET Multiview](https://github.com/humansensinglab/basket-multiview) | 合成バスケ。読込・評価用候補で、実写デモの代替にはしない |

[配布アーカイブ](https://huggingface.co/datasets/BestWJH/VRU_Basketball/tree/main)はgz約779MB、dg約727MB、long360約8.12GB。展開後容量・VRAMとは別の値。VRUの視点0/10/20/30を評価用に保持し、姿勢・時刻・歪み補正・点群の対応を確認する。一般の単眼中継から見えない側面を復元することは初期対象外。

## 再構成モデルの選定

| 候補 | 特性と初期判断 |
| --- | --- |
| [LocalDyGS](https://github.com/WuJH2001/LocalDyGS) | VRU向け前処理・学習例があり第一候補 |
| [Wuらの4D Gaussians](https://guanjunwu.github.io/4dgs/) | 時空間特徴から変形。端末へ変形処理を移す方法を比較 |
| [Dynamic 3D Gaussians](https://dynamic3dgaussians.github.io/) | Gaussianを時間方向に追跡。明示属性・差分の比較候補 |
| [GIFStream](https://github.com/XDimLab/GIFStream) | 時間依存の特徴streamと圧縮学習の参照候補 |

LocalDyGSの[VRU手順](https://github.com/WuJH2001/LocalDyGS#22-for-other-multi-view-dynamic-scenes-eg-meetroom-vru-dataset)に合わせ、20フレームから検証する。区間接続と端末性能は別に評価する。[LocalDyGSの研究・評価用途制限](https://github.com/WuJH2001/LocalDyGS/blob/main/LICENSE)・[Dynamic 3D Gaussiansの条件](https://github.com/JonathonLuiten/Dynamic3DGaussians/blob/main/LICENSE.md)と依存描画コードのlicenseを個別に確認し、自作コードのMITを外部素材へ適用しない。

## モバイル向け表現を先に確かめる

クラウドで明示的なGaussian属性へ変換する方式を、端末でネットワーク全体を動かす方式と比較する。

- 位置・回転・scale・色・不透明度の時間変化と視点依存性を保つ。
- 点の追加・削除・対応関係を定義する。配列番号の一致だけで時間差分を取らない。
- 時刻・視点別に変換誤差、ボール・手足、区間境界の不連続を確認する。

## 配信用データの構成案

| 要素 | 内容 |
| --- | --- |
| manifest | 版、時刻・座標系、区間、品質、依存関係、checksum、推奨視点・操作範囲 |
| 初期状態 | 区間途中から独立に復号するための属性・共有情報 |
| 時系列・品質別資産 | 対応が定義できれば差分、未成立なら明示サンプル。品質ごとに独立復号 |
| preview | 自由視点資産の準備中に表示する動画・画像 |

HTTPオブジェクトを独立区間で取得・切替する。追加点だけで品質を上げる互換性は仮定しない。現在clipを優先し、次clipのpreview・先頭区間だけを限定先読み、離脱時に取得・復号を取消しGPU資源を解放する。[ABR調査](streaming-research.md)・[共通契約](../streaming-contract.md)を参照。

## 同時視聴規模に必要な最適化

数千〜数万人は設計目標で、容量未検証。次を分離して測る。

- CDN共有：immutable revision、不要なcache key分散の抑制。
- 通信：ABR、時間・品質分割、取消、限定先読み。空間分割は視点変更時の品質も評価。
- 端末：圧縮bytesに加え、復号時間、展開後RAM/VRAM、描画負荷。
- 制御・生成：水平分散するGo APIと、別に並列数・費用を制限するGPUジョブ。

## 圧縮と高速化の順序

| 順序 | 採用判断 |
| --- | --- |
| 1. 明示属性の基準出力 | 時刻・視点別に変換誤差を説明できる |
| 2. 属性量子化 | 転送・メモリ削減と画質条件を両立 |
| 3. 分割取得 | 操作開始を短縮し、停止を増やさない |
| 4. 描画最適化 | 同品質・同端末でframe-timeを改善 |
| 5. 時間圧縮 | 容量削減が復号・ランダムアクセス費用に見合う |
| 6. 圧縮を考慮した学習 | 学習費用を含めて有効性を示す |

全方式の実装予定ではなく、測定で次を選ぶ順序。[HHIの動画codec方式](https://fraunhoferhhi.github.io/CodecGSDemo/)は発展候補で、属性精度・GPU転送・対応decoderを別途評価する。

## モバイルの選定理由

Flutter WebでUIを先に作り、Gaussian描画を分離する。[Vulkan](https://developer.android.com/games/develop/vulkan/overview)・[ADPF](https://developer.android.com/games/optimize/adpf)と[ブラウザのWebGPU](https://developer.chrome.com/blog/new-in-webgpu-121)を候補に、対象実機で機能・メモリ・持続性能を比較する。native化だけを高速化の根拠にしない。

## クラウドとGoの役割

Go APIがfeed・job ID・公開状態・manifest参照を管理し、[CustomJob](https://docs.cloud.google.com/vertex-ai/docs/training/create-custom-job)が入力検証→前処理→学習→評価→packingを行う。[Cloud CDN](https://docs.cloud.google.com/cdn/docs/setting-up-cdn-with-bucket)からモデルを直接配信し、長時間学習をHTTP内で待たない。

[Workflows](https://docs.cloud.google.com/workflows/docs/overview)・[Firestore](https://docs.cloud.google.com/firestore/native/docs/overview)は当時の進行・状態管理候補。重複起動防止、管理認証、有限予算、最小権限を設計し、入力・commit・image・config・出力の版を対応づける。[goroutine](https://go.dev/doc/effective_go#concurrency)の利用だけで性能向上を主張しない。

## 比較条件と数値目標案

VRU gzの同一区間・評価視点・時刻・解像度で比較し、モデル変換誤差と圧縮誤差を分離する。以下は初期仮説で、達成値・固定要件ではない。

| 指標 | 初期案・条件 |
| --- | --- |
| 描画 | 720p相当30fps。frame-timeのp50/p95も記録 |
| 操作開始 | cold cache、20Mbps・RTT50msで2秒以内。preview開始とは別計測 |
| 初期取得 | 3MB以下。20Mbps×2秒の理論量約5MBから準備時間を確保 |
| 画質 | 圧縮前比PSNR差0.5dB以内を出発点に、小物体・時間方向も確認 |
| メモリ | 実機選定後に予算化。CPU/GPU・作業領域・先読みのpeakを記録 |
| 持続性 | 同一シーン列5分で発熱・frame-time・メモリ増加を測る案 |

容量には索引・共有情報・区間開始状態も含め、全量と短時間視聴時の実転送を分ける。学習速度は同じGPU・入力・画質で費用も比較する。

## 同時視聴の容量検証計画

1人の取得量B bits/s、同時N人なら視聴者側の概算帯域はB×N。originの負荷はhit率・区間サイズ・人気分布に依存する。

| 対象 | 指標 |
| --- | --- |
| 視聴・資産 | preview/自由視点TTFF、再バッファp95、品質別bytes、取消・未視聴分の転送 |
| CDN/origin | request/bytes hit率、帯域/RPS、cold/warm cache・人気集中 |
| API・端末 | API p95/エラー、frame-time、peak RAM/VRAM、復号・連続再生負荷 |
| GPU | 並列上限、待ち時間、実行時間、費用 |

数百→数千→数万の段階負荷を例とし、実行前に予算・region・quota・中止条件を確定する。未測定のSLOを達成値にしない。

## モノレポの構成案

API、ML、端末描画、形式契約、infra、benchmarksを分離する構想。実際の配置は[製品README](../../README.md#資料と開発)を参照。生データ・大型モデル・秘密情報をGitへ置かず、出典・hash・条件・設定を管理する。開発と最終提出の違いは[提出案内](../submission.md)に集約する。

## 次の段階の区切り

端末・期限・人数・GPU予算・画質条件を確定し、gzの20フレームで前処理→再構成→held-out評価→端末変換・動的描画をつなぐ。ここまでを最初の判断点とし、成立後に量子化・分割取得・描画を個別比較してクリップを延ばす。素材や固定視点動画への置換を同じ自由視点体験の成功とは扱わない。
