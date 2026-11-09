# Temporal training comparison profiles

Two optional profiles test specific reconstruction hypotheses. They are candidates for GPU comparison; their implementation and CPU checks do not establish a visual improvement. The default remains `--quality-profile baseline`.

The existing VRU Basketball `gz` baseline used input SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`, 20 synchronized times at 25 fps, 34 cameras, 480×270 RGB, seed 0, 30 training cameras, four held-out cameras, and 12,000 iterations. The observed Tokyo T4 run reported packed mean PSNR 27.618 dB, SSIM 0.91133, reversed-time PSNR 22.177 dB, 259,797 stored primitives, and 159.86 seconds for the training loop. These are measurements of that run, not a guarantee for another dataset or machine. The supplied initial geometry may incorporate held-out cameras; held-out RGB pixels are excluded from optimization.

| Profile | Hypothesis | Deliberate change |
| --- | --- | --- |
| `baseline` | Reference for both comparisons | Existing initialization, RNG draws, loss, learning rates, primitive budgets, and evaluation |
| `motion-prior` | Free velocities can introduce drift in static background | Add a velocity penalty where training images show little temporal variation |
| `local-lifetime` | Broad lifetime support can leave moving-player ghosts | Distribute initial centre times over the interval and initialize sigma to one quarter of its duration |

Both candidates retain the same number of seed primitives, spatial seed positions, RGB, initial spatial scales, peak opacity, zero velocity, iteration limit, and evaluation images. No points are removed, duplicated at initialization, or additionally quantized. `local-lifetime` changes which subset initially contributes strongly at each time, so it is a different initialization despite the equal primitive count. Its centre times use the existing shuffled point order, without extra RNG draws. Each timestamp has nearby centres with approximately peak support, including interval endpoints.

## Motion prior

The worker computes per-pixel RGB variance across the input times separately for each **training** camera, with float32 Welford accumulation. Activity is `clip(sqrt(mean(RGB variance)) / 0.05, 0, 1)` for RGB in `[0,1]`. The threshold is fixed; it is not fitted using held-out images. Shadows, lighting changes, occlusion, and calibration errors can also produce activity, so this is not a foreground mask or optical-flow estimate.

At each sampled training camera/time, the current Gaussian centres are projected into that camera. Bilinear activity samples provide a fixed detached weight `(1 - activity)^2`; offscreen and behind-camera primitives receive zero weight. The extra loss is:

```text
0.01 * mean(weight * ||velocity * interval_duration||^2)
```

Only velocity receives this penalty's gradient. Camera projection and activity are detached, preventing the regularizer from moving primitives toward active image locations. The activity is sampled again after topology changes, so no separate primitive labels can lose alignment during densification or pruning.

The expected tradeoff is stronger background stability versus the risk of suppressing motion where image variation is weak or correspondence is inaccurate. This must be judged from held-out results and the first/middle/last images.

## Reproduction and acceptance

For the already approved input, pass either candidate flag to the same worker invocation used for baseline:

```text
--mode dynamic --steps 12000 --quality-profile motion-prior
--mode dynamic --steps 12000 --quality-profile local-lifetime
```

Non-baseline profiles are rejected for static training. Omitting the flag preserves baseline behavior. The backend's trusted preset chooses the profile; it is not a public free-form input.

Metrics include `quality_profile`, the exact profile configuration, activity camera indices, initial count multiplier, and `quality_setup_seconds`. Compare setup plus training/evaluation time, stored primitive count, raw/export bytes, mean and per-image float/export PSNR and SSIM, matched/reversed-time PSNR, endpoint motion, and diagnostic images. Activity setup time is reported separately rather than hidden from the cost comparison. Torch peak allocation includes the activity maps in the training allocation baseline.

The two GPU comparisons use the same input checksum, 480×270 resolution, seed 0, split, 20 times, and 12,000 iterations. Run one GPU at a time within the existing 30-minute job and shared attempt/cost limits. A profile is not adopted merely because its mean PSNR rises: inspect moving players for ghosts and static background for drift, and report any size/time regressions. The integrator records actual GPU results and chooses whether either candidate is useful.

CPU checks verify baseline initialization/RNG compatibility, equal primitive counts and temporal support, training-only activity maps, and finite velocity-only regularizer gradients. Run the NumPy contract suite and the explicit complete ML suite described in [dynamic-gs-training.md](dynamic-gs-training.md). No reconstruction-quality claim follows from those checks.
