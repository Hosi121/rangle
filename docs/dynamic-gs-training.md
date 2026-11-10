# Joint temporal Gaussian training

`--mode dynamic --steps 12000` fits shared time-dependent Gaussians to calibrated multiview video. Static remains the default (6,000-step ceiling, `.splat`); dynamic permits 12,000 steps with the same 30-minute job/publication boundary. See [generation](generation.md) for the service workflow.

## Representation and optimization

Each primitive stores position, linear velocity, centre time, positive temporal sigma, peak-opacity logit, RGB, spatial scale and quaternion. At seconds `t`:

```text
position(t) = position + velocity * (t - centre_time)
opacity(t) = sigmoid(peak_logit) * exp(-0.5 * ((t - centre_time) / sigma)^2)
```

This independent RGB implementation uses gsplat 1.5.3 and the conditional 4D representation in [FreeTimeGS §3.1](https://arxiv.org/html/2506.05348v1), matching `temporal-material.js`. It does not reproduce the full paper or its quality/speed. The [UVG-GS study](archive/gaussian-training.md) uses different data/supervision.

Each step samples one time/training camera and jointly updates all fields. Separate Adam optimizers feed gsplat `DefaultStrategy`; split/duplicate/prune operations preserve alignment of temporal rows and optimizer moments.

| Setting | Baseline |
| --- | --- |
| Initialization | ≤100,000 supplied points, zero velocity, centre at interval midpoint, sigma = duration; endpoint opacity ≈88% of peak |
| Loss | `0.8 L1 + 0.2 (1 - SSIM)` + `1e-4` mean spatial scale |
| Early penalty | `1e-3` mean peak opacity, linearly decaying to zero in first quarter; quality benefit unestablished |
| Temporal bounds | Sigma: half a frame interval to four durations; centres: minus one to twice duration |
| Refinement / hard limit | Stop by step 6,000 or pre-refinement count 250,000; reject >500,000 primitives |

Other profiles and measured effects are in [training quality](training-quality.md).

## Input contract

Dynamic NPZ adds `frame_times` to the seven static keys. Cameras are synchronized and fixed across time; static image limits are unchanged.

| Key | Dynamic shape and type |
| --- | --- |
| `images` | uint8 `[time, view, height, width, 3]`; 2–32 times, 4–64 views, height 64–1080, width 64–1920 |
| `frame_times` | float64 `[time]`; finite, increasing, uniform seconds from zero; ≤240 fps |
| `viewmats` | finite float `[view,4,4]`; rigid OpenCV world-to-camera |
| `Ks` | finite float `[view,3,3]`; positive focal lengths |
| `xyz` | finite float `[point,3]`; 100–1,000,000 supplied points |
| `rgb` | uint8 `[point,3]` |
| `train_indices` | unique integer indices; ≥3 cameras |
| `heldout_indices` | nonempty unique indices, disjoint from training |

Limits: compressed input 256 MiB, declared ZIP expansion 1 GiB, dynamic images 512 MiB. NPY dtype/shape/bytes are checked before allocation; duplicate/unexpected ZIP members, object arrays and invalid time/camera/split schemas fail. Use attributed, calibrated VRU input; restricted UVG-GS assets are outside this public worker workflow. [Preprocessing](dataset-preprocessing.md) defines source/provenance requirements.

## Export and publication

RGS1 has a 32-byte little-endian header: `RANGL4D1`, count, stride 56, frame count, float32 fps/duration `(frames - 1) / fps`, zero flags. Each row is the existing 32-byte `.splat` position/scale/RGBA8/quaternion8 plus float32 velocity `[3]`, centre time, sigma and peak opacity. Position is at centre time; IDs/order and zero-alpha rows are retained.

`model.rgs` uses `RANGL4Z1` with reversible byte-column shuffle/gzip, retaining existing RGB/quaternion quantization. Limits are 500,000 primitives / 32 MiB. Internal gzip means **no HTTP `Content-Encoding: gzip`**; static `.splat` keeps its outer gzip. See [compression](gaussian-compression.md).

```json
{
  "version": 1,
  "frameRate": 25,
  "frameCount": 20,
  "temporal": { "url": "model.rgs" },
  "camera": { "...": "the existing calibrated camera" },
  "attribution": { "...": "the existing source attribution" }
}
```

Model, metrics and diagnostic WebP uploads precede `scene.json`. Every upload uses generation-match zero; success is logged only after all complete.

## Evidence and checks

Every held-out camera/time is evaluated without optimizing its pixels. Supplied geometry/calibration may still incorporate held-out source cameras. Metrics retain checksums, mode/seed/times/splits, train/evaluation duration, count, Torch peak allocation and per-image/mean float and export PSNR/SSIM. Export evaluation uses viewer ties-to-even RGBA8 opacity; gsplat evaluation does not establish pixel-identical WebGL rasterization.

Temporal diagnostics record per-frame sample counts, nonzero velocities/speed, endpoint displacement/opacity/render differences and matched-versus-reversed-time PSNR. Full-image reversed-time scores can be background-dominated; they do not establish motion quality. WebP diagnostics use every held-out camera at first/middle/last times.

Two dependency profiles:

- `ml/tests`: NumPy-only CI contracts for NPZ/time/static schemas, RGS1 bounds, actual scene-last publication with fake storage, and JS codec when Node is available; no Google SDK/cloud access.
- `ml/tests/full_ml`: explicit Torch/gsplat suite for temporal gradients, topology/optimizer alignment and opacity/rounding. Missing dependencies fail; default contract discovery excludes it.

Build from repository root and run both without networking/GPU access:

```sh
docker build --platform=linux/amd64 --target trainer -f ml/Dockerfile \
  -t rangle-trainer:local .
docker run --rm --network=none --entrypoint python \
  -v "$PWD:/workspace:ro" -w /workspace -e PYTHONPATH=ml \
  rangle-trainer:local -m unittest discover -s ml/tests -v
docker run --rm --network=none --entrypoint python \
  -v "$PWD:/workspace:ro" -w /workspace -e PYTHONPATH=ml:ml/tests \
  rangle-trainer:local -m unittest discover -s ml/tests/full_ml -v
ruff check ml/rangle_training ml/tests
```

Host alternatives for the contract suite and Node oracle:

```sh
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
PYTHONPATH=ml:ml/tests python3 -m unittest test_temporal.TemporalPackingTests -v
```

CPU checks establish contracts, not GPU speed or reconstruction quality.

## Verified GCP run — 2026-11-09

One deployed job ran on Tokyo Spot T4: VRU Basketball gz, 20 times `0.00–0.76s` at 25 fps, 480×270, 30 train/4 held-out cameras. Frame zero matched the static input exactly; source SHA is in metrics. Worker [e1228df](https://github.com/Hosi121/rangle/tree/e1228dff0014b9e2b92176f2993b7fdcf491c8fa/ml), trainer digest `sha256:ab7f56fb8cf970226a553e6664df5f5fc92100ad63ca6246a1b3d80ef985c161`.

| Measurement | Result |
| --- | --- |
| Joint training | 12,000 iterations; 159.86 seconds |
| Evaluation | 80 held-out images; 5.88 seconds |
| Vertex creation to success | 8 minutes 3 seconds, including preparation/publication |
| Stored primitives | 259,797 |
| Float PSNR / SSIM | 27.626 dB / 0.91157 |
| Export PSNR / SSIM | 27.618 dB / 0.91133 |
| Export versus reversed-time targets | 22.177 dB |
| RGS1 / compressed bytes | 14,548,664 / 12,106,161 |
| Torch peak allocated GPU bytes | 420,947,456 |

[Scene](https://app.rangleplay.stream/demo/?clip=generated-be37a1b9e8eea50af0e6a440f0fa4778) · [metrics](https://media.rangleplay.stream/generated/be37a1b9e8eea50af0e6a440f0fa4778/metrics.json). Held-out diagnostics show movement; Python/JS decoded raw SHA matched and viewer manifest/primitive preparation accepted the downloaded model. Hands-on GUI validation is separate.

This verifies one short calibrated sequence. Moving players remain soft/ghosted; ≈12.1 MB is substantial. No other-dataset, single-time-model or paper-quality comparison is claimed; the geometry caveat applies. Presentation endpoints are scheduled to stop after November 14.
