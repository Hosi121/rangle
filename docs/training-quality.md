# Temporal training comparison

On the fixed VRU Basketball `gz` input, `local-lifetime` recorded the highest held-out mean PSNR/SSIM in one run per profile. Its packed PSNR was 27.814041 dB, versus baseline 27.618057 dB: a 0.195984 dB difference. `motion-prior` reduced those mean metrics. These results support selecting `local-lifetime` for this input; preset adoption and deployment are separate changes.

## Measured conditions and results

All three runs used the same input SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`, 20 times at 25 fps, 34 cameras, 480×270 RGB, 99,330 initial primitives, seed 0, and 12,000 iterations on a Tokyo Tesla T4. Torch was 2.6.0+cu124 and gsplat 1.5.3. The same 30 cameras supplied training pixels; held-out cameras `[0,10,20,30]` were evaluated at every time, giving the same 80 images. Scores below are full-image means from the decoded export, including its per-time RGBA8 alpha evaluation.

| Profile | Packed PSNR (dB) | Packed SSIM | Final Gaussians | Model bytes | Training loop (s) |
| --- | ---: | ---: | ---: | ---: | ---: |
| `baseline` | 27.618057 | 0.911333 | 259,797 | 12,106,161 | 159.863 |
| `motion-prior` | 27.420715 | 0.910226 | 261,168 | 12,058,991 | 173.735 |
| `local-lifetime` | 27.814041 | 0.915371 | 250,637 | 11,626,506 | 152.087 |

The floating-model PSNRs were 27.625994, 27.427700, and 27.822283 dB respectively, preserving the same ordering. Reversed-time packed PSNRs were 22.177066, 22.114258, and 22.029459 dB; these reuse each render against the same camera's target at the reversed time. This is a full-frame time-correspondence diagnostic, without a player ROI.

Model bytes include export compression. Baseline used the previous Z1 encoder; the comparison runs used the smallest lossless strategy. The 3.962% baseline-to-`local-lifetime` size difference also includes changed training and primitive count. The isolated **same-model** compression result is only 12,106,161 → 11,967,872 bytes, or 1.142%; see [the compression comparison](temporal-compression-comparison.md).

Training-loop time excludes setup, evaluation, provisioning, and publication. The motion-prior activity setup took 1.062 s. Evaluation took 5.883 s for baseline, 6.036 s for motion-prior, and 23.826 s for local-lifetime; the last run included 18.100 s of optional precision-comparison work. The lower local-lifetime loop time is not an end-to-end job speedup measurement.

Each profile has **one trial**, with no repeated-seed confidence estimate. Full-image averages can hide moving-player ghosts or local background drift; these runs do not establish that either defect is resolved. Held-out pixels are excluded from optimization, but supplied initial geometry/calibration may incorporate held-out source cameras. The result does not establish quality on another sequence or sport.

## What the profiles change

| Profile | Hypothesis | Change from baseline |
| --- | --- | --- |
| `baseline` | Reference | Existing initialization, RNG draws, loss, learning rates, and budgets |
| `motion-prior` | Free velocities can cause background drift | Add a velocity penalty at locations with little training-image temporal variation |
| `local-lifetime` | Broad lifetime support can leave motion ghosts | Distribute initial centre times across the interval and initialize sigma to one quarter of its duration |

The candidates retain the same initial primitive count, spatial seed positions, RGB, spatial scales, peak opacity, zero velocity, iteration limit, and evaluation images. No initialization points are removed or duplicated. Local-lifetime uses the existing shuffled point order without additional RNG draws. It changes the subset strongly contributing at each time; endpoints still have nearby centres with approximately peak support.

Motion-prior computes float32 Welford RGB variance across times for each **training camera only**. Its fixed activity map is `clip(sqrt(mean(RGB variance)) / 0.05, 0, 1)`, for RGB in `[0,1]`. Current centres are projected into the sampled camera; detached bilinear activity samples weight this added loss:

```text
0.01 * mean((1 - activity)^2 * ||velocity * interval_duration||^2)
```

Offscreen and behind-camera primitives receive zero weight. Only velocity receives the penalty's gradient. Re-sampling after topology changes avoids separate primitive labels losing alignment. Lighting, shadows, and occlusion can also produce activity; the map is an appearance heuristic, without foreground or flow supervision. This trial's lower mean scores do not support an overall quality benefit from this prior.

## Reproduction and precision evaluation

The compared candidate invocations added these flags to the same bounded worker job:

```text
--mode dynamic --steps 12000 --quality-profile motion-prior
--mode dynamic --steps 12000 --quality-profile local-lifetime --evaluate-compression
```

The worker CLI still defaults to `baseline`; non-baseline profiles are rejected for static training. Trusted presets choose the profile. Metrics retain `quality_profile`, exact configuration, setup time, train/evaluation splits, model bytes, and the existing per-image float/export results. CPU checks for initialization/RNG compatibility, temporal support, and training-only prior gradients are described in [dynamic-gs-training.md](dynamic-gs-training.md).

`--evaluate-compression` adds inference-only comparisons on the same 80 held-out images, with no further training. The [measured FP16 comparison](temporal-compression-comparison.md) reports both average and local maximum pixel differences. **FP16 remains unadopted; only the original wire representation is served.** Candidate model files are never written or published. First/middle/last comparison WebP files show ground truth, original wire, and candidate.
