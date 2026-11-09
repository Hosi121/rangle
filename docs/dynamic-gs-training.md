# Joint temporal Gaussian training

The training worker accepts `--mode dynamic --steps 12000` to fit one shared set of time-dependent Gaussian primitives to a calibrated multiview video. Static training remains the default, with its existing 6,000-step ceiling and `.splat` output. Dynamic training has a 12,000-step ceiling and uses the same storage, checksum, immutable publication, and 30-minute job boundary. No additional service or runtime dependency is required.

## Representation and optimization

Each primitive has a position, linear velocity, centre time, positive temporal sigma, peak-opacity logit, RGB color, spatial scale, and quaternion. At time `t` in seconds:

```text
position(t) = position + velocity * (t - centre_time)
opacity(t) = sigmoid(peak_logit) * exp(-0.5 * ((t - centre_time) / sigma)^2)
```

This is a conditional 4D Gaussian with a linear spatial trajectory and a Gaussian temporal envelope. The equations follow the representation in [FreeTimeGS §3.1](https://arxiv.org/html/2506.05348v1) and match the viewer's `temporal-material.js`. The worker is a small independent RGB implementation using gsplat 1.5.3. It does not reproduce the paper's full method or claim its reported quality or speed. The prior [local study](gaussian-training.md) uses different data and supervision; its results are not evidence for this worker's quality.

Each step samples a time and a training camera, then updates the same shared primitive arrays. Position, color, scale, rotation, velocity, centre time, temporal sigma, and peak opacity are optimized together. Frames are not trained as independent 3D models. Every primitive array has its own Adam optimizer and is passed to gsplat `DefaultStrategy`, so duplication, splitting, and pruning apply the same row transformation to all temporal fields and optimizer moments.

Initialization uses up to 100,000 supplied points, zero velocity, centre time at the interval midpoint, and temporal sigma equal to the interval duration. All input times therefore have nonzero support; endpoint opacity starts at about 88% of peak. The loss is `0.8 L1 + 0.2 (1 - SSIM)`, with `1e-4` mean spatial-scale regularization. A `1e-3` mean peak-opacity penalty decays linearly to zero during the first quarter of training to discourage early saturation. This is an implementation choice, without an established quality benefit. Temporal sigma is bounded from half a frame interval to four interval durations; centre times stay between minus one and twice the duration. Refinement stops by step 6,000, or when the pre-refinement population reaches 250,000; a hard 500,000-primitive boundary rejects an oversized result.

## Input contract

Dynamic NPZ contains exactly the seven static keys plus `frame_times`. Static archives and their four-dimensional image budget are unchanged.

| Key | Dynamic shape and type |
| --- | --- |
| `images` | uint8 `[time, view, height, width, 3]`; 2–32 times, 4–64 views, height 64–1080, width 64–1920 |
| `frame_times` | float64 `[time]`; finite, increasing, uniform seconds starting at zero; frame rate at most 240 fps |
| `viewmats` | finite float `[view, 4, 4]`; rigid OpenCV world-to-camera transforms |
| `Ks` | finite float `[view, 3, 3]`; positive focal lengths |
| `xyz` | finite float `[point, 3]`; 100–1,000,000 supplied seed points |
| `rgb` | uint8 `[point, 3]` |
| `train_indices` | unique integer camera indices; at least three |
| `heldout_indices` | nonempty unique integer camera indices, disjoint from training |

The compressed download limit remains 256 MiB, total declared ZIP expansion is at most 1 GiB, and dynamic image storage is at most 512 MiB. Before loading arrays, the worker validates each NPY header, dtype, shape, and declared byte count. Duplicate or unexpected ZIP entries, object arrays, unsupported members, excessive allocation claims, and invalid time/camera/split schemas are rejected. Camera calibration is shared across times. This contract describes synchronized, fixed cameras.

Use VRU input with the existing attribution and calibration. Restricted UVG-GS research assets or experimental harnesses are not inputs to this public worker workflow.

## Export and publication

The output is the viewer's existing RGS1 format. Its 32-byte little-endian header contains `RANGL4D1`, primitive count, stride 56, frame count, float32 fps, float32 duration `(frames - 1) / fps`, and zero flags. Each row retains the existing 32-byte `.splat` position/scale/RGBA8/quaternion8, followed by float32 velocity `[3]`, centre time, sigma, and peak opacity. Position is measured at the primitive's centre time. All primitive IDs and row ordering are retained, including zero-alpha rows.

`model.rgs` uses `RANGL4Z1`: the same header plus byte-column shuffle and gzip of the rows. This compression is reversible; the existing RGB/quaternion quantization remains. A model cannot exceed 500,000 primitives or 32 MiB. The HTTP object has **no `Content-Encoding: gzip`**, because gzip is internal to RGS1.

The scene uses the existing temporal viewer contract:

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

The worker uploads model, metrics, and diagnostic WebP images before `scene.json`, using generation-match zero for every upload. Completion is logged only after every upload succeeds. Static `.splat` publication retains its existing outer gzip and HTTP encoding.

## Evidence and checks

Every held-out camera is evaluated at every time, with its pixels excluded from optimization. The supplied initial geometry and calibration may still incorporate those source cameras; the metrics retain this caveat. Metrics report data/model checksums, mode, seed, frame times, train/evaluation splits, training/evaluation time, primitive count, and Torch peak allocated memory. Per-image and mean PSNR/SSIM are recorded for both the floating model and the decoded export. Export evaluation includes the viewer's ties-to-even RGBA8 opacity at each requested time. This checks stored-model semantics with gsplat, rather than claiming pixel-identical WebGL rasterization.

Temporal evidence includes training sample counts per frame, nonzero velocity count, speed, endpoint displacement/opacity difference, and held-out first/last render differences. The same held-out renders are also compared against reversed-time targets from the same camera, reporting matched-time versus reversed-time mean PSNR for float and export. This is a full-image diagnostic without a motion ROI; static background can dominate the scores, and a positive difference does not by itself establish motion quality. Diagnostic WebP images are saved for each held-out camera at the first, middle, and last times.

The test suites have two explicit dependency profiles. The default `ml/tests` discovery is the lightweight contract suite: it needs NumPy only and covers unsafe NPZ/time schemas, unchanged static contracts, RGS1 layout and bounded decompression, scene-last publication/failure behavior, and the existing JavaScript codec when Node is available. Publication tests inject a fake storage client into the worker entry point, exercising its actual publication logic without importing the Google SDK or contacting storage. This is the profile run by `ml-contract` CI.

```sh
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
```

The separate `ml/tests/full_ml` suite requires the complete Torch/gsplat runtime and verifies gradients through all temporal parameters, gsplat topology and optimizer alignment, and export opacity equations including ties-to-even rounding. It must be run explicitly as part of ML validation; missing dependencies fail this suite rather than skipping its tests. The directory is outside default contract discovery so a NumPy-only CI job does not pretend to validate training dependencies.

Build the trainer from the repository root, then run both test profiles without GPU access or networking:

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

CPU validation does not establish GPU rendering performance or reconstruction quality. The JavaScript codec oracle runs when Node is available; it can also run separately on the host:

```sh
PYTHONPATH=ml:ml/tests python3 -m unittest test_temporal.TemporalPackingTests -v
```

## Verified GCP run — 2026-11-09

The deployed creation API completed one dynamic job on a Tokyo Spot T4. The input was VRU Basketball gz, with 20 synchronized times from 0.00 to 0.76 seconds at 25 fps, 480×270 pixels, 30 training cameras, and 4 held-out cameras. The source bundle SHA256 is recorded in the public metrics. Its first time matches the previously validated static input pixel-for-pixel.

The worker source was [e1228df](https://github.com/Hosi121/rangle/tree/e1228dff0014b9e2b92176f2993b7fdcf491c8fa/ml), deployed as trainer image digest `sha256:ab7f56fb8cf970226a553e6664df5f5fc92100ad63ca6246a1b3d80ef985c161`.

| Measurement | Result |
| --- | --- |
| Joint training | 12,000 iterations; 159.86 seconds |
| Evaluation | 80 held-out images; 5.88 seconds |
| Vertex creation to success | 8 minutes 3 seconds, including preparation and publication |
| Stored primitives | 259,797 |
| Float PSNR / SSIM | 27.626 dB / 0.91157 |
| Export PSNR / SSIM | 27.618 dB / 0.91133 |
| Export compared against reversed-time targets | 22.177 dB |
| RGS1 bytes / compressed bytes | 14,548,664 / 12,106,161 |
| Torch peak allocated GPU memory | 420,947,456 bytes |

[Open the generated scene](https://app.rangleplay.stream/demo/?clip=generated-be37a1b9e8eea50af0e6a440f0fa4778) or inspect its [metrics](https://media.rangleplay.stream/generated/be37a1b9e8eea50af0e6a440f0fa4778/metrics.json). The first, middle, and last held-out images show the players moving. Python and the existing JavaScript decoder produced the same raw SHA256 from the actual downloaded model, and the viewer's manifest and primitive preparation accepted it. The object has no HTTP content encoding; compression is internal to RGS1.

This validates one calibrated, short sports sequence and its stored-model contract. Hands-on GUI testing is separate. The RGB linear-motion baseline still has visible softness and artifacts around moving players, and approximately 12.1 MB remains substantial for this short interval. No comparison against a different dataset, a single-time model, or a paper's reported quality is claimed. The evaluation geometry/calibration caveat above still applies. Public presentation endpoints are scheduled to stop after November 14; the implementation and measurements remain in this document.
