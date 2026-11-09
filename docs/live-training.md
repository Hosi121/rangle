# Incremental training from arrived calibrated video

`rangle_live` decodes synchronized recorded camera videos at their original PTS and updates one persistent temporal Gaussian model from arrived image windows. It writes immutable RGS1/scene snapshots and an atomic live index. This is a recorded-source replay test of incremental generation, with no RTMP receiver, camera-clock synchronization, cloud publisher, or trained-model warm start.

The current measurements establish functional generation before source completion. They do **not** establish production quality or a 30-second latency guarantee. Moving players remain blurred and ghosted.

## Run and input

Use the existing complete trainer environment plus the separate [preprocessing dependencies](dataset-preprocessing.md). No new dependency is added to the trainer image. `--help` parses before importing the runtime, Torch, or OpenCV.

```sh
PYTHONPATH=ml python3 -m rangle_live \
  --source-manifest /path/to/live-source.json \
  --source-root /path/to/calibrated-videos \
  --output-dir /path/to/new-output-directory \
  --session-id basketball-live --title 'バスケットボール ライブ検証' \
  --source-seconds 10 --seed-available-after 0.8 \
  --bootstrap-steps 300 --steps-per-update 300 \
  --decode-workers 8 --max-runtime-seconds 180
```

The manifest is the existing source-manifest v1, using either `calibrated-manifest` or `vru-colmap`, with one local video per view. It supplies the resolution, fps, calibration, training/held-out split, source/license statements, and colored seed cloud. `frames.indices` remains the offline-preparation selection; only its count supplies the live window length. Live playback always starts at recorded frame 0, at the original `frames.fps` cadence, and `--source-seconds` controls its range. Index values do not set the live start, end or sampling stride. The shared validator enforces 4–64 views, at least three training and one held-out camera, output width 64–1920 / height 64–1080, and at most 512 MiB of uint8 RGB per window. Live windows require 2–32 frames.

Paths stay inside the source root. Calibration/seed checksums are verified before ingestion; checksum declarations for other files are rejected. Full video files are not read or hashed before capture starts. Actual frame PTS and native dimensions must match the declared synchronized calibration and cadence. Each camera's distortion map is precomputed directly at the target resolution, with one source-to-target remap per frame. The rig/world normalization is computed **once**, from training camera centres, and remains fixed across every snapshot.

Source and runtime bounds are finite, at most 1,800 seconds each. Decode workers are bounded to 1–32 (default 8); only 4 and 8 were measured below. Bootstrap/update budgets are bounded to 1–1,000 and 1–2,000 steps. `--seed-available-after` is required: the caller declares the seed prior's temporal support instead of the worker inferring it from a filename. The bootstrap id/title are defaults that callers can replace. Source labels, URLs and licenses come from the manifest; the VRU bootstrap manifest must explicitly use `source.license: "CC BY 4.0"` for the agreed publication metadata.

An existing compatible gsplat extension may be selected with `--cuda-extension /path/to/gsplat_cuda.so`; its SHA256 is logged. This supports the validated local environment without committing binaries or changing gsplat's production optimizer. The command logs JSON with `service="rangle-live"`, `severity`, `event`, and UTC `time`; failures are recorded once at the command boundary.

## Incremental state and bounded work

A producer requests a decoded frame only once its source PTS has arrived. A ring retains one window; a single pending window replaces older pending work while the consumer trains. There is no growing work queue. Skipped windows are not reconstructed as a continuous stream; only selected windows are optimized and published. `dropped_windows` counts these replacements. The current training window is immutable, so later arrivals cannot change its optimization targets. The last complete window is offered at source completion, including an overlapping tail when necessary.

The consumer initializes shared position, velocity, colour, spatial scale/quaternion, centre time, temporal width, and opacity parameters once. It retains their Adam optimizers, moments, RNG, global step count, and gsplat `DefaultStrategy` state between windows. Refinement may change rows through the strategy's existing optimizer-aware operations. A training step samples only a current-window time and a training camera. Held-out pixels are used for evaluation only.

The representation and rasterizer reuse [joint temporal training](dynamic-gs-training.md). Initialization uses the `local-lifetime` profile with the same point count, zero velocity, centre times spread across the window and sigma equal to one quarter of its duration. The loss is `0.8 L1 + 0.2 (1 - SSIM)`, with spatial-scale regularization and an early decaying peak-opacity penalty. Refinement stops by global step 3,000 or at a pre-refinement count of 125,000; a 250,000-primitive hard bound rejects an oversized update. These choices are a small streaming approximation, without a claim to reproduce a research system.

Models use window-local seconds. When the window start advances by `delta`, the warm shift is:

```text
means_new = means_old + velocity * delta
centre_time_new = centre_time_old
x_new(t_local) = x_old(t_local + delta)
opacity_new(t_local) = opacity_old(t_local)
```

This deliberately resets the lifetime phase within the new window; it does not preserve an absolute global lifetime envelope. Additive translation retains parameter identities and Adam moments. Snapshot transitions can therefore pop or ghost. Each scene retains the same camera/world convention, and the publisher rejects a changed camera within a session.

The supplied VRU `downsample_0_20` cloud is a **precomputed author prior** that may contain frames through 20 (0.8 seconds) and held-out camera information. The first window is admitted only after source time 0.8 has arrived. This does not establish fully causal geometry: only the optimization image stream is causal. No later seed cloud or offline trained model is loaded.

## Local output and publication boundary

```text
output/index.json
output/live-assets/<id>/<sequence:06d>-<model-SHA256-first16>/model.rgs
output/live-assets/<id>/<sequence:06d>-<model-SHA256-first16>/metrics.json
output/live-assets/<id>/<sequence:06d>-<model-SHA256-first16>/scene.json
output/run-summary.json
output/diagnostics/heldout-<view>-frame-<source-frame>.webp
```

The output directory must be new. Model and local metrics are written first, scene next, then the revision directory is renamed into place; the index is atomically replaced last. Failed asset writes cannot add a partial revision to the index. Sequence numbers start at 1, the index retains the latest 32 segments, and index/model limits are 512 KiB / 32 MiB. Assets remain immutable; retention of old asset directories is a separate publisher/cleanup concern.

The index is `{"version":1,"sessions":[session]}`. Session fields are `id`, `title`, `state` (`starting`, `live`, `ended`, `failed`), `started_at`, `updated_at`, `target_latency_seconds`, `latest_input_time_seconds`, `dropped_windows`, `source`, and `segments`. `source` has `label`, `url`, `license`, and `caveat`. Each segment contains exactly:

```text
sequence, start_seconds, end_seconds, scene_url,
model_sha256, model_bytes, captured_at, available_at,
training_seconds, mean_packed_psnr, mean_packed_ssim
```

Segment times are absolute source PTS; model time runs from zero to `end_seconds - start_seconds`. `scene_url` is `/live-assets/<id>/<revision>/scene.json`, `temporal.url` is exactly `model.rgs`, and scene attribution label/source/license equal the session title/source URL/license. RGS1 uses the existing lossless RLE gzip encoding, with no extra quantization or point removal and no HTTP `Content-Encoding: gzip`. VOD keeps its smallest-of-two encoding policy.

Live updates prioritize encoding time. For the 13 frozen snapshots measured below, RLE produced byte-for-byte identical files to the smallest-of-two choice. On the three final models, a separate CPU check measured 0.045–0.049 seconds for RLE versus 1.011–1.058 seconds for smallest-of-two, with equal decoded bytes. These are single encoding observations, not whole-pipeline speedups; other inputs can produce larger RLE files. The training table below predates this encoding-only change; the final delivery integration must measure the combined pipeline again.

Only `model.rgs` and `scene.json` are publication assets. Metrics, provenance and the three final packed held-out RGB diagnostics stay local. Diagnostics are written after snapshot availability timing, at the first/middle/last time of the final window using its first held-out view. The index exposes only the agreed aggregate metrics. This module performs no cloud mutations or deployment.

## Finite local measurement, 2026-11-09

One 10-second VRU Basketball gz source, 250 frames at 25 fps, 34 cameras, 480×270 RGB, 30 training / 4 held-out views, 20-frame windows, seed 0, and 99,330 initial points. Hardware: local NVIDIA RTX PRO 500 Blackwell Generation Laptop GPU; Torch 2.10.0+cu128 / gsplat 1.5.3. No GCP GPU or B300 was used. Exactly three training configurations were run, with bootstrap 300 steps throughout. Final manifest generalization, structured logging and diagnostic plumbing were CPU-checked afterwards; the measured dimensions, fps, windows and optimizer equations were unchanged, and no further training trial was run.

Frame 0 RGB bytes, normalized view matrices and target intrinsics exactly matched the existing validated VRU NPZ (SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`). Each snapshot evaluates both float and actual packed output on all 20 times × 4 held-out views. The final window is source frames 230–249 for all three runs; earlier selected windows differ because pending work is replaced. Thus this is a streaming latency/quality tradeoff, not a controlled fixed-step quality benchmark or an improvement over the offline 12,000-step baseline.

| Decode workers / update steps | Snapshots / dropped | First output since capture start | Mean / max capture→local | Final packed PSNR / SSIM | Final global steps / Gaussians / bytes |
| --- | ---: | ---: | ---: | ---: | ---: |
| 4 / 300 | 6 / 7 | 15.432 s | 31.704 / 48.863 s | 20.8830 / .704484 | 1,800 / 128,335 / 5,804,274 |
| 8 / 300 | 4 / 9 | 14.539 s | 22.075 / 30.882 s | 20.7988 / .685863 | 1,200 / 127,833 / 5,765,117 |
| 8 / 600 | 3 / 10 | 14.682 s | 25.290 / 35.797 s | 21.5217 / .711016 | 1,500 / 127,669 / 5,775,973 |

All three produced the first model while source decoding was still in progress. The faster decoder still took 27.897 seconds to read the 10-second source in the 8/300 run. Its per-view decode wall times summed to 177.552 seconds versus 12.111 seconds for remap/colour conversion; these overlapping per-view wall sums are **not CPU time**. The decoder was the main source of capture/arrival lag. An independent 10-frame-set CPU profile measured 1.593 seconds with four workers and 1.030 seconds with eight, using the same resampling pixels.

For the 8/300 run, per-update training took 6.258–7.827 seconds, lossless packing/compression 1.113–1.509 seconds, and float+packed held-out evaluation 0.611–0.729 seconds. The final 8/600 update took 12.833 / 1.209 / 0.593 seconds respectively. That final model had 127,669 velocities above magnitude `1e-6`; mean absolute first/last packed-render difference across the four held-out views was .043059 on RGB `[0,1]`. These establish temporal dependence, not accurate motion reconstruction.

Latency uses a session UTC/monotonic epoch plus original source PTS as the simulated capture timestamp. `capture→local` includes decoder lateness, pending-window wait, model update, export, evaluation and local scene/index availability. `arrival→local` excludes decoder lateness and is also recorded; it must not substitute for capture latency. Startup (1.077–1.607 seconds) is reported separately. Cloud upload, HTTP delivery, client polling, decoding and display are **excluded**. The best measured maximum was 30.882 seconds, so a strict 30-second or end-to-end target remains unproven. These are single trials; visible player ghosting and author-supplied geometry limit the quality claim.

## Checks

```sh
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
ruff check ml/rangle_live ml/tests/test_live.py ml/tests/full_ml/test_live_model.py
ruff format --check ml/rangle_live ml/tests/test_live.py ml/tests/full_ml/test_live_model.py
# Complete ML environment; Torch CPU, no GPU execution:
PYTHONPATH=ml:ml/tests python3 -m unittest discover -s ml/tests/full_ml -v
```

The lightweight suite checks pacing before image access, no future windows, replacement/drop counts, cancellation/deadlines, generic manifest dimensions/splits/budgets, seed admission, publication ordering/failures, immutable revisions, fixed camera metadata, exact index fields, retention bounds, finite CLI arguments and help without runtime imports. The existing complete-ML CPU suite analytically verifies warm-shift position/lifetime behavior while retaining actual Adam state and parameter identities. All nine tests also passed in the existing Torch 2.6 trainer image with networking and GPU access disabled. Local diagnostic encoding uses the trainer's existing Pillow dependency. No tests are skipped to accommodate the lightweight environment.
