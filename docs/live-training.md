# Incremental training from arrived calibrated video

`rangle_live` replays synchronized recorded camera videos at original PTS, updates one persistent temporal Gaussian model from arrived windows, and writes immutable RGS1/scene snapshots plus an atomic index. It has no RTMP receiver, clock synchronization, cloud publisher or trained-model warm start. Short runs establish generation before source completion; player blur/ghosting and a sustained 30-second target remain unresolved. Cloud operation is documented in [cloud LIVE](cloud-live.md).

## Run and input

Use the complete trainer plus separate [preprocessing dependencies](dataset-preprocessing.md); the trainer image gains no dependency. `--help` precedes runtime/Torch/OpenCV imports.

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

| Input / option | Contract |
| --- | --- |
| Manifest | Source-manifest v1, `calibrated-manifest` or `vru-colmap`, one local video/view, resolution/fps/calibration/splits/source terms/colored seed. VRU publication metadata requires explicit `source.license: "CC BY 4.0"`. ID/title defaults are replaceable. |
| Time | Only the `frames.indices` **count** sets window length. Replay starts at frame 0 with original `frames.fps`; `--source-seconds` sets duration. Index values do not set live start/stride/end. Actual PTS/dimensions must match calibration/cadence. |
| Bounds | 4–64 views, ≥3 train/1 held-out; width 64–1920, height 64–1080; 2–32 frames/window; uint8 RGB ≤512 MiB/window. Source/runtime each ≤1,800s. |
| Work budgets | Decode workers 1–32, default 8 (only 4/8 measured); bootstrap 1–1,000 and update 1–2,000 steps. `--seed-available-after` is required to declare seed temporal support. |
| Paths/checksums | Paths stay in source root. Verify calibration/seed hashes before ingestion; other checksum declarations fail. Do not pre-read/hash full videos. |

Distortion maps are precomputed at target resolution for one remap/frame. Normalization is computed once from training camera centres and fixed across snapshots. Optional `--cuda-extension /path/to/gsplat_cuda.so` selects an existing compatible extension and logs its SHA, without committing binaries or changing the production optimizer. JSON logs use `service="rangle-live"`, severity/event/UTC time; boundary failures are logged once.

## Incremental state and bounded work

Decode requests wait for source PTS. One ring window and one replaceable pending window bound work; the active training window is immutable. Replaced windows increment `dropped_windows` and are never reconstructed as a continuous stream. Source completion offers the last complete window, with overlapping tail if needed.

Position/velocity/RGB/scale/quaternion/centre/sigma/opacity, Adam moments, RNG, global steps and `DefaultStrategy` state persist between windows; optimizer-aware refinement keeps row alignment. Steps sample only current-window training pixels; held-out pixels evaluate only.

The [joint-training](dynamic-gs-training.md) representation uses `local-lifetime`: unchanged initial count, zero velocity, distributed centres, sigma=duration/4; `0.8 L1 + 0.2 (1 - SSIM)`, scale regularization and decaying opacity penalty. Refinement stops by global step 3,000 or pre-refinement count 125,000; >250,000 points fail. This streaming approximation does not reproduce a research system.

Window-local warm shift by `delta`:

```text
means_new = means_old + velocity * delta
centre_time_new = centre_time_old
x_new(t_local) = x_old(t_local + delta)
opacity_new(t_local) = opacity_old(t_local)
```

Translation retains parameter identities/moments but resets lifetime phase in each window; absolute lifetime is not preserved and transitions can pop/ghost. Camera/world convention stays fixed; publication rejects a changed session camera.

VRU `downsample_0_20` is an author-precomputed prior that may include frames through 20 (0.8s) and held-out camera information. Admission waits until source time 0.8s. Only optimization images are causal; geometry is not proven causal. No later seed cloud or offline trained model is loaded.

## Local output and publication boundary

```text
output/index.json
output/live-assets/<id>/<sequence:06d>-<model-SHA256-first16>/model.rgs
output/live-assets/<id>/<sequence:06d>-<model-SHA256-first16>/metrics.json
output/live-assets/<id>/<sequence:06d>-<model-SHA256-first16>/scene.json
output/run-summary.json
output/diagnostics/heldout-<view>-frame-<source-frame>.webp
```

Output must be new. Write model/metrics, then scene; rename revision into place and atomically replace index last. Failures cannot index partial revisions. Sequence starts at 1; index retains 32 segments and is ≤512 KiB; models ≤32 MiB. Old asset-directory cleanup is separate.

Index shape is `{"version":1,"sessions":[session]}`. Session fields: `id`, `title`, `state` (`starting`, `live`, `ended`, `failed`), `started_at`, `updated_at`, `target_latency_seconds`, `latest_input_time_seconds`, `dropped_windows`, `source` (`label`, `url`, `license`, `caveat`) and `segments`. Each segment contains exactly:

```text
sequence, start_seconds, end_seconds, scene_url,
model_sha256, model_bytes, captured_at, available_at,
training_seconds, mean_packed_psnr, mean_packed_ssim
```

Segment times are absolute PTS; model time is zero to `end_seconds - start_seconds`. `scene_url` is `/live-assets/<id>/<revision>/scene.json`, `temporal.url` exactly `model.rgs`. Attribution label/source/license equals title/source URL/license.

Live uses lossless RLE Z1, no added quantization/removal or HTTP Content-Encoding; VOD keeps smallest-of-two. On 13 frozen snapshots RLE equalled smallest-of-two bytes. Three final-model CPU checks observed RLE 0.045–0.049s versus 1.011–1.058s, with identical raw bytes. These single encode observations do not establish pipeline speedup; other inputs may grow. The local training table predates this encoder change.

Only model/scene are publication assets. Metrics/provenance/three final packed diagnostics remain local; index exposes agreed aggregates. Diagnostics use first held-out view at first/middle/last final-window times, after availability timing. This module performs no cloud mutation/deployment.

## Finite local measurement, 2026-11-09

One 10s VRU Basketball gz source: 250 frames/25fps, 34 cameras, 480×270 RGB, 30 train/4 held-out, 20-frame windows, seed 0, 99,330 initial points. Local NVIDIA RTX PRO 500 Blackwell Generation Laptop GPU, Torch 2.10.0+cu128/gsplat 1.5.3; no cloud GPU. Exactly three configurations, bootstrap 300 each. Later manifest/logging/diagnostic generalization was CPU-checked without further training or changed dimensions/fps/windows/optimizer equations.

Frame 0 RGB, normalized view matrices and K exactly matched validated NPZ SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`. Every snapshot evaluates float/actual packed on 20×4 held-out images. All final windows are frames 230–249; earlier windows differ due to replacement. This is a latency/quality tradeoff, not a fixed-step quality comparison against offline 12,000 steps.

| Decode workers / update steps | Snapshots / dropped | First output since capture start | Mean / max capture→local | Final packed PSNR / SSIM | Final global steps / Gaussians / bytes |
| --- | ---: | ---: | ---: | ---: | ---: |
| 4 / 300 | 6 / 7 | 15.432 s | 31.704 / 48.863 s | 20.8830 / .704484 | 1,800 / 128,335 / 5,804,274 |
| 8 / 300 | 4 / 9 | 14.539 s | 22.075 / 30.882 s | 20.7988 / .685863 | 1,200 / 127,833 / 5,765,117 |
| 8 / 600 | 3 / 10 | 14.682 s | 25.290 / 35.797 s | 21.5217 / .711016 | 1,500 / 127,669 / 5,775,973 |

All first models preceded decode completion. In 8/300, decoding 10s took 27.897s; overlapping per-view wall sums were 177.552s decode versus 12.111s remap/color (**not CPU time**). Decode dominated arrival lag. A separate 10-frame-set CPU profile took 1.593s with four workers and 1.030s with eight, using the same pixels.

For 8/300, per-update training took 6.258–7.827s, packing 1.113–1.509s, and float+packed evaluation 0.611–0.729s. Final 8/600 times were 12.833/1.209/0.593s respectively. Its 127,669 final velocities exceeded `1e-6`; mean absolute first/last packed-render RGB difference was .043059 (`[0,1]`, four held-out views). These establish time dependence, not accurate motion.

Simulated capture timestamps use session UTC/monotonic epoch plus source PTS. `capture→local` includes decoder lateness, queue wait, training/export/evaluation/index availability; `arrival→local` excludes decoder lateness. Startup 1.077–1.607s is separate. Upload/HTTP/polling/client decode/display are excluded. Best maximum 30.882s misses strict 30s; single trials and supplied geometry limit generalization.

## Checks

### Verified cloud delivery, 2026-11-09

Source `19085e206f67c37b65db86be86fa433e5114c2f8`, same 10s rig, 8 workers, 300 bootstrap/update, RLE; local training and independent publisher ran concurrently, without cloud GPU. Four models reached GCS/Cloud CDN, first HTTP-visible before decode completion; nine pending windows dropped. Capture uses the last sample of each 0.76s window.

| Measurement | Observed result |
| --- | ---: |
| Capture start → first local model | 14.104 s |
| Maximum capture → local model | 26.803 s |
| Maximum capture → CDN-verifiable publication | 29.810 s |
| Maximum capture → HTTP observer's first retrieval | 30.236 s |
| Final packed held-out PSNR / SSIM | 20.5904 dB / .674379 |
| Final compressed model | 5,774,550 bytes |

Observer metadata used the separate one-second CDN route (`max-age=1`, app CORS); API/CDN agreed at completion. All four models passed HTTP scene/timeline/size/SHA/Range 206 and Python/JS raw-byte equality.

This short integration excludes browser display and sustained SLOs. Encoding changed scheduling/intermediate windows, so it is not an isolated speedup comparison. Ghosting/gaps remain; [#89](https://github.com/Hosi121/rangle/issues/89) tracks throughput/quality/longer input. The ended test retains its [last model](https://app.rangleplay.stream/demo/?live=basketball-live-20261109). Current cloud deployment/results: [cloud LIVE](cloud-live.md).

### Automated checks

```sh
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
ruff check ml/rangle_live ml/tests/test_live.py ml/tests/full_ml/test_live_model.py
ruff format --check ml/rangle_live ml/tests/test_live.py ml/tests/full_ml/test_live_model.py
# Complete ML environment; Torch CPU, no GPU execution:
PYTHONPATH=ml:ml/tests python3 -m unittest discover -s ml/tests/full_ml -v
```

Lightweight contracts cover PTS pacing/no future access, replacement/cancellation/deadlines, manifest budgets, seed admission, immutable scene-last publication/failure, camera/index/retention bounds and finite CLI/help without runtime imports. Complete-ML CPU tests verify warm-shift/lifetime equations with real Adam identities/state. All nine also passed in the Torch 2.6 trainer without network/GPU; missing dependencies are not skipped. Diagnostics use existing Pillow.
