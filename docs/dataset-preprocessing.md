# Calibrated dataset preprocessing

`rangle_preprocessing` converts local synchronized camera images or videos, supplied camera calibration, and a colored seed cloud into the existing trainer NPZ. It also writes a provenance sidecar. It performs no cloud access, reconstruction, training, or asset download. A new dataset normally needs a calibrated manifest, without a new code path. The VRU adapter translates the authors' COLMAP layout into the same common pipeline.

```sh
PYTHONPATH=ml python3 -m rangle_preprocessing \
  --manifest /path/to/source.json \
  --source-root /path/to/local/source-root \
  --output /path/to/prepared/dataset.npz
```

`--source-root` defaults to the manifest directory. All input paths inside the manifest must be relative to that root; traversal, escaping symlinks, network paths, missing files, and output overwrites are rejected. Both `dataset.npz` and `dataset.json` must be absent. Complete validated files are published with exclusive hard links, so existing files are never replaced.

The core pipeline and contract tests require NumPy only. RGB `.npy` images that already match the requested pinhole calibration pass through unchanged. Video decoding, PNG/JPEG input, distortion correction, and resizing require the **separate** local preprocessing dependencies:

```sh
python3 -m venv /path/to/preprocessing-env
/path/to/preprocessing-env/bin/pip install --require-hashes --only-binary=:all: \
  -r ml/rangle_preprocessing/requirements.lock
```

The lock targets Linux x86_64 / CPython 3.11–3.14 and pins NumPy 2.4.2 / OpenCV headless 4.13.0.92 independently of the GPU trainer. Its wheel hashes come from official PyPI release metadata. No changes to the trainer image or dependency lock are needed.

## Source manifest v1

The top-level keys are `version`, `adapter`, `source`, `frames`, `output`, `seed`, `normalization`, and the adapter's `views` or `vru` payload. Optional `checksums` maps relative source paths to expected lowercase SHA256 values. Unknown fields, duplicate JSON keys, nonfinite numbers, unused checksum paths, and checksum mismatches fail validation.

`source` requires nonempty `name`, public HTTPS `url`, `license`, `attribution`, `calibration_license`, and `initialization_scope`. These are the caller's explicit source statements, retained in the output; the tool does not infer legal permission from an adapter name or URL. Record calibration/seed licensing separately when it differs from the raw imagery. `initialization_scope` must state whether held-out cameras contributed to the supplied seed geometry or calibration, or whether this is unknown. Restricted research datasets must not be silently treated as approved public/cloud input.

`frames.indices` selects 1–32 uniformly spaced source frame indices and `frames.fps` declares the synchronized source rate, at most 240 fps. A single selected frame produces the static seven-key schema. Multiple frames produce the temporal eight-key schema. Output time starts at zero and uses `(source_index - first_index) / source_fps`; selecting every second source frame therefore halves the output frame rate. Videos are decoded sequentially from the beginning, with no approximate keyframe seeks. Every view must use the same selected indices and source fps. Image lists are already aligned with those selected indices. The tool does not estimate camera clock offsets or synchronize unrelated recordings.

`output.width` / `height` set the target resolution within the worker's existing bounds. `seed` requires a relative `path`, explicit `max_points` (100–1,000,000), and integer `seed`. Clouds larger than the chosen maximum are sampled deterministically, and source/selected counts are recorded. Supported clouds are:

- An NPZ containing exactly finite float `xyz [N,3]` and uint8 `rgb [N,3]`, with bounded NPY headers and no pickle.
- A vertex-only PLY in ASCII or binary little-endian form, with scalar float `x/y/z` and uint8 `red/green/blue`. Optional scalar normal or other properties are ignored. Faces, lists, unsupported types, and inconsistent sizes are rejected.

The common normalization is `training-camera-centres`: centre is the mean of training camera centres and scale is their maximum distance from that centre. The same affine change applies to seed positions and all camera translations. Held-out camera positions do not choose this normalization. `none` explicitly preserves coordinates and records centre zero / scale one. Calibration and seed geometry may still incorporate held-out source cameras; normalization does not remove that caveat.

## Generic calibrated adapter

Set `adapter` to `calibrated-manifest` and provide 4–64 `views`, with unique string or integer IDs, `split` equal to `train` or `heldout`, and at least three training plus one held-out view. The view list determines array indices; it is never implicitly sorted or split. Each view has:

```json
{
  "id": "camera-0",
  "split": "train",
  "media": { "images": ["camera-0/frame-000.npy", "camera-0/frame-001.npy"] },
  "calibration": {
    "width": 960,
    "height": 540,
    "K": [[730, 0, 480], [0, 730, 270], [0, 0, 1]],
    "world_to_camera": [[1, 0, 0, 0], [0, 1, 0, 0], [0, 0, 1, 0], [0, 0, 0, 1]],
    "distortion": [0, 0, 0, 0]
  }
}
```

Use `media.video` instead of `media.images` to identify a video. The image list length must equal the selected frame count. NPY images are uint8 RGB `[height,width,3]`; 8-bit RGB PNG and JPEG are decoded to RGB; alpha-bearing PNG is rejected instead of silently choosing a compositing background. Camera transforms use the [COLMAP/OpenCV world-to-camera convention](https://colmap.github.io/format.html): quaternion-derived rotations, when applicable, are Hamilton `w,x,y,z`, and positive camera Z points forward. The generic manifest supplies the actual rigid matrix, so no axis convention is guessed.

Calibration `K` is measured at the declared original dimensions. Optional `distortion` accepts OpenCV's 4, 5, or 8 coefficient pinhole model. Optional `rectified` supplies a different target pinhole `K`, `width`, and `height` when the source calibration provides it. Otherwise the target pinhole calibration starts from the source `K`. The target intrinsics are scaled independently in x/y to the output dimensions, and [OpenCV initUndistortRectifyMap](https://docs.opencv.org/4.13.0/d9/d0c/group__calib3d.html) plus linear remapping performs the conversion. The tool does not optimize new intrinsics or apply an unrequested additional undistortion to already calibrated media.

A complete manifest wraps the views with the shared fields:

```json
{
  "version": 1,
  "adapter": "calibrated-manifest",
  "source": {
    "name": "Your synchronized capture",
    "url": "https://example.org/capture-info",
    "license": "REPLACE_WITH_ACTUAL_LICENSE",
    "attribution": "REPLACE_WITH_REQUIRED_ATTRIBUTION",
    "calibration_license": "REPLACE_WITH_CALIBRATION_AND_SEED_LICENSE",
    "initialization_scope": "REPLACE_WITH_GEOMETRY_AND_HELDOUT_CAMERA_SCOPE"
  },
  "frames": { "indices": [0, 1], "fps": 25 },
  "output": { "width": 480, "height": 270 },
  "seed": { "path": "seed/cloud.npz", "max_points": 100000, "seed": 0 },
  "normalization": "training-camera-centres",
  "views": []
}
```

Replace `views` with the calibrated view records, including the explicit held-out split; the empty list is intentionally invalid. The source statement placeholders must also be replaced with the actual source terms. Do not infer a license for an additional dataset from an existing one.

## VRU COLMAP adapter

Set `adapter` to `vru-colmap`, retain all shared fields, and replace `views` with:

```json
{
  "vru": {
    "videos": "raw/videos",
    "calibration": "calibration",
    "heldout_view_ids": [0, 10, 20, 30]
  }
}
```

The adapter reads `frame000000/distorted/sparse/0/cameras.bin` (COLMAP OPENCV model 4), `frame000000/sparse/0/cameras.txt` (supplied PINHOLE intrinsics), and `frame000000/sparse/0/images.txt` (`camN` names and camera poses). It sorts numeric view IDs and resolves `videos/train/N.mp4` / `videos/test/N.mp4` according to the explicit held-out IDs. The seed path remains an explicit shared `seed.path`; no dataset-specific directory or personal machine path is compiled into the package.

The [authors' VRU Basketball dataset card](https://huggingface.co/datasets/BestWJH/VRU_Basketball) labels the raw dataset CC-BY-4.0 and documents the standard held-out view IDs. Supplied calibration/seed provenance must be recorded separately; the tool does not assume every author-linked archive has a separate matching license.

## Output, validation, and equivalence

The output remains the existing seven-key static or eight-key temporal NPZ. The common worker loader validates it before publication: camera rotations/intrinsics, finite bounded geometry, disjoint split, time schema, 512 MiB temporal image budget, 1 GiB total archive expansion, and 256 MiB compressed download limit. Static image shape/limits remain unchanged. Source NPY/seed headers are inspected before allocation; source image/video dimensions must match calibration.

The sidecar schema `rangle-prepared-dataset-v1` records source/license/attribution statements, manifest SHA, relative source file paths with roles/SHA/bytes, view IDs and split, source frames/fps and output times/fps, normalization, seed/counts, per-array SHA256, whole NPZ SHA/bytes, dependency versions, and the initialization/held-out caveat. Relative paths describe provenance without recording a developer's absolute environment. Keep source media, prepared NPZ, and private manifests outside Git.

On the approved VRU `gz` input, the reusable CLI was run against the original 34 videos, supplied COLMAP calibration, and colored seed PLY. The regenerated 20×34×270×480 RGB NPZ was byte-identical to the earlier prepared input: 171,336,472 bytes, SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`. All eight arrays matched, and frame zero exactly matched the previously validated static input. Seed 0, 99,330 points, 30 training / four held-out cameras, normalization, and `0,0.04,…,0.76` times were retained. This is preprocessing equivalence on that input, not a quality claim for arbitrary media.

A second cached sequence was also converted through the generic adapter from calibrated RGB images, matching K/poses, and an untrained seed PLY, rather than loading a canonical NPZ. This research-restricted material was checked on local CPU only; its data, output NPZ, images, and metadata remain private, with no cloud or public transfer. The check used existing calibrated RGB derivatives because the original RGBA PNGs are rejected by the explicit RGB contract. It establishes input-conversion coverage, not successful training on a second sport or sequence.

The NumPy-only CPU suite covers real generic NPY conversion, source checksums and path boundaries, strict schemas, normalization projection invariance, static/temporal contracts and frame stride, deterministic seed sampling, COLMAP adapter parsing, provenance, and overwrite refusal:

```sh
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
ruff check ml/rangle_preprocessing ml/rangle_training ml/tests
```

The explicit media profile requires the preprocessing dependencies and fails when they are absent. It exercises real lossless PNG/video RGB decoding, exact frame selection, synchronized-fps rejection, and target-intrinsics resizing:

```sh
PYTHONPATH=ml:ml/tests python3 -m unittest discover -s ml/rangle_preprocessing/tests -v
```
