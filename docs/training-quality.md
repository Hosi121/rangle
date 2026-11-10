# Temporal training comparison

On one fixed-input trial per profile, `local-lifetime` had the highest held-out mean: packed PSNR **27.814041 dB versus baseline 27.618057 dB (+0.195984)**. `motion-prior` reduced mean quality. This supports local-lifetime for this input; adoption/deployment are separate decisions.

## Measured conditions and results

All runs: VRU Basketball `gz`, SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`, 20 times/25fps, 34 cameras, 480×270 RGB, 99,330 initial points, seed 0, 12,000 iterations, Tokyo Tesla T4, Torch 2.6.0+cu124/gsplat 1.5.3. The same 30 cameras trained; held-out `[0,10,20,30]` supplied 80 evaluations. Means include decoded export's per-time RGBA8 alpha.

| Profile | Packed PSNR (dB) | Packed SSIM | Final Gaussians | Model bytes | Training loop (s) |
| --- | ---: | ---: | ---: | ---: | ---: |
| `baseline` | 27.618057 | 0.911333 | 259,797 | 12,106,161 | 159.863 |
| `motion-prior` | 27.420715 | 0.910226 | 261,168 | 12,058,991 | 173.735 |
| `local-lifetime` | 27.814041 | 0.915371 | 250,637 | 11,626,506 | 152.087 |

Float PSNR: 27.625994 / 27.427700 / 27.822283 dB. Reversed-time packed PSNR: 22.177066 / 22.114258 / 22.029459 dB, comparing each render to its camera's reversed-time target without a player ROI.

The 3.962% baseline-to-local-lifetime size reduction includes training/count changes and previous-Z1 versus smallest-lossless encoding. Isolated same-model compression saves **1.142%**, 12,106,161→11,967,872B; see [compression comparison](temporal-compression-comparison.md).

Loop time excludes setup/evaluation/provisioning/publication. Motion-prior setup: 1.062s. Evaluation: baseline 5.883s, motion-prior 6.036s, local-lifetime 23.826s (including optional precision comparison 18.100s). Loop differences are not end-to-end speedups.

Limits: one seed/trial per profile; full-image means can hide player ghosts/background drift. Held-out pixels are excluded from optimization, but supplied geometry/calibration may include those cameras. No defect-resolution or other-sequence claim follows.

## What the profiles change

| Profile | Hypothesis / change |
| --- | --- |
| `baseline` | Existing initialization, RNG, loss, learning rates and budgets |
| `motion-prior` | Penalize velocity where training images show little temporal variation, to test background drift |
| `local-lifetime` | Spread centres across the interval; sigma = duration/4, to test broad-lifetime ghosts |

Candidates retain initial count/positions/RGB/scales/peak opacity, zero velocity, iteration limit and evaluation images. No points are removed/duplicated. Local-lifetime reuses shuffled point order without new RNG draws; endpoints retain nearby centres with near-peak support.

Motion-prior uses training-camera-only float32 Welford RGB variance and activity `clip(sqrt(mean(RGB variance)) / 0.05, 0, 1)` for RGB `[0,1]`. Detached bilinear samples at projected current centres weight:

```text
0.01 * mean((1 - activity)^2 * ||velocity * interval_duration||^2)
```

Only velocity gets gradients; offscreen/behind-camera points get zero weight. Re-sampling keeps topology alignment. Lighting/shadows/occlusion also cause activity, so this is an appearance heuristic without flow/foreground supervision; measured scores do not support an overall benefit.

## Reproduction and precision evaluation

Candidate flags for the same bounded worker job:

```text
--mode dynamic --steps 12000 --quality-profile motion-prior
--mode dynamic --steps 12000 --quality-profile local-lifetime --evaluate-compression
```

CLI default remains `baseline`; static rejects other profiles. Trusted presets choose profiles. Metrics retain `quality_profile`, configuration/setup/splits/bytes/per-image float/export results. CPU checks are described in [joint training](dynamic-gs-training.md).

`--evaluate-compression` compares the same 80 images without training. [FP16 results](temporal-compression-comparison.md#fp16精度候補の評価未採用) retain average and local maximum differences. **FP16 is unadopted; original wire only is served.** Candidate models are neither saved nor published; first/middle/last WebP comparisons contain GT, original and candidate.
