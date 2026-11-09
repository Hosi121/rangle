# 固定13windowの学習比較

2026-11-09、[観測flowの比較案](gs-quality-compression-2026.md)をofflineで検証した。FP32 fused Adam候補は共通3000step区間で学習時間が16.77%短かった。flowの平均画質には小差があるが準備に18.07秒を要し、最終windowのPSNRは改善していない。各条件1回の結果であり、両候補とも採用・既定値変更はしていない。

## 条件と範囲

GPUの機種・構成は非公開。全条件で同じGPUを使った比較であり、他環境の速度を推定する根拠にはしない。

同じ検証GPU 1枚、PyTorch 2.8.0+cu129 / CUDA 12.9 / gsplat 1.5.3を使い、baseline→flow→fusedの固定順で各1回、上限600秒・計3runで完了した。実装は`76accbfd`。fusedの切替は検証用runnerだけで、サービスやCLIには追加していない。

入力は承認済みVRU Basketball gzの250frames、34camera、480×270、25fps。30 train / heldout `[0,10,20,30]`、random seed 0、著者供給`downsample_0_20`由来の初期99,330点とcalibrationを固定した。初期parameter全欄・正規化rig/K・frame 0 RGBのSHA256と、学習frame/viewのsampling列は3条件で一致した。著者geometry/calibrationにはheldout cameraや初期時刻の情報が含まれ得るため、完全に未知のgeometryを使う評価とは称さない。

20frameの固定windowを13本使用した。最初はsource frame 1–20（PTS .04–.80秒）、以後21–40,…,221–240、最後は230–249。最終windowは直前と11frame重なる。bootstrap300＋各更新300、合計3900stepsで、寿命式・warm shift・densification・LR・optimizer式を保った。動画は先にCPU decodeし、学習とflowは指定window内だけを参照する。到着をpaceするlive実験や約30秒の配信遅延測定ではない。

- **baseline**: 既存`local-lifetime`とFP32 Adam。
- **flow**: camera ID 1/11のtrain画像だけへOpenCV 4.13 DIS_MEDIUM / CPU1 threadを適用。隣接frameのforward/backward整合≤1px、canonical行から最大4096点、追加RNGなし。補助項は`0.01 × mean_xy(Huber(pixel差, δ=1px)) / width`。投影位置・confidence・寿命supportをdetachし、velocityだけへ勾配を流す。heldout/future画像は使わず、occlusionの可視性モデルは追加していない。226stepsで補助項を評価した。MoRGSの再現ではない。
- **fused**: 初期状態からFP32 Adamを`fused=True, foreach=False`で作り直した。LR、eps=1e-15、betas=(.9,.999)、初期parameterは同じ。計算順・丸めは同一でなく、点数も変わったためbit一致ではない。

## 画質と容量

各windowの同じ20時刻×4 heldout、80画像を評価した。下表の平均は全13windowのpacked RGS1評価で、最終PSNRは最後のwindowだけの値。temporal MAEはRGB `[0,1]`で`mean(abs((pred[t+1]-pred[t])-(GT[t+1]-GT[t])))`を測った。

| 条件 | 平均PSNR (dB) | 平均SSIM | temporal MAE | 最終PSNR (dB) |
| --- | ---: | ---: | ---: | ---: |
| baseline | 21.9701 | .751647 | .0111300 | 21.7073 |
| flow | 22.0514 | .753982 | .0110731 | 21.7066 |
| fused | 22.0450 | .752504 | .0111204 | 21.6773 |

flowの平均PSNR差は+.0812dBだが、最終PSNRはbaselineを上回っていない。全画面の平均は静止背景に影響され、選手の残像・局所の動き・主観画質の改善を示すとは限らない。heldout view 0 / source frame230,240,249の診断画像でも、全条件に選手のぼけ・細長い残像が残り、改善と判断できる差は確認できなかった。3時刻・1viewの限定観察であり、ROIや全面的な知覚評価は実施していない。

| 条件 | 最終Gaussian数 | 出力13本の合計bytes | Torch peak allocated (MiB) |
| --- | ---: | ---: | ---: |
| baseline | 127,988 | 72,685,082 | 805.382 |
| flow | 128,072 | 72,736,024 | 869.172 |
| fused | 127,976 | 72,682,479 | 804.848 |

既存RGS1とbyte shuffle＋gzip（Z1）のままで、新しい量子化・点削減・wire変更はない。byte差には学習後の値・点数の差が含まれ、圧縮単独の効果ではない。Torch値はallocated memoryであり、CUDA context等を含むGPU全体の使用量ではない。

## 時間とtransient

初回warmupは16×16・1 Gaussianのforward/backwardで、実解像度・点数のoptimizer/strategy stepは含まない。baselineのwindow 1/3は15.9566/12.3372秒、flowは15.2193/1.8960秒、fusedは2.8794/2.1191秒だった。baselineのwindow 1はtopology更新前。最初の更新可能stepは600で、window 3中に点数が増え、125,000点の安全弁へ達した次stepでrefinementを停止する。正確な到達stepやkernel別時間は記録していない。

初回runtime・配列/optimizer state再配置・cacheの寄与を分離できないため、各runの遅いwindowを個別に除外せず、全条件で共通のwindow 4–13（global step900–3899、3000steps）を追加集計した。この範囲の点数は各run内で固定だが、run間では異なる。

| 条件 | W4–13学習 (s) | baseline比 | 全13学習 (s) | flow準備wall (s) | loop全体 (s) |
| --- | ---: | ---: | ---: | ---: | ---: |
| baseline | 18.3825 | — | 48.6321 | 0 | 58.2247 |
| flow | 17.5314 | −4.63% | 36.6758 | 18.0698 | 63.0441 |
| fused | 15.2993 | −16.77% | 22.4267 | 0 | 29.4988 |

学習時間は画像stack/H2D後のCUDA同期から測り、flow準備は含まない。loopはfit・flow準備・評価・符号化・診断・local出力を含むが、共通の事前decode 27.37秒と各runのstartup（7.05/5.18/7.74秒）は含まない。flowは今回、準備費用を含むloopでbaselineより長かった。

全13学習時間やloopの約2倍の差を、そのままfused optimizerの効果と断定できない。共通範囲でも点数・opacity・可視性による仕事量が異なり、純粋なoptimizer kernel比較ではない。固定順の各1回では、process間cache、clock、温度、実行ばらつきを取り除けず、統計的な再現性は未確認。

最終RGS3本は既存JS `decodeTemporal→prepareTemporal`によるfinite/range/count/timeline検証もofflineで通過した。ブラウザGUIの検証は含まない。

根拠は取得後SHA256照合済みのprivate `COMPARISON.json`と各条件の`summary.json`。画像・重み・元素材はGitにも公開領域にも置いていない。今回はoffline検証で、GCP・既存サービスへの接続、モデル公開、merge、deployは行っていない。
