# Calibrated dataset preprocessing

`rangle_preprocessing` converts local synchronized images/videos, supplied calibration and a colored seed cloud into trainer NPZ plus a provenance sidecar. It performs no reconstruction, training, download or cloud access. New sources use a calibrated manifest; the VRU adapter translates the authors' COLMAP layout into this pipeline.

```sh
PYTHONPATH=ml python3 -m rangle_preprocessing \
  --manifest /path/to/source.json \
  --source-root /path/to/local/source-root \
  --output /path/to/prepared/dataset.npz
```

`--source-root` defaults to the manifest directory. Input paths must be relative and stay inside it: traversal, escaping symlinks, network paths and missing files are rejected. Both `dataset.npz` and `dataset.json` must be absent; validated output is published with exclusive hard links.

The core pipeline/contracts need NumPy only. Matching pinhole RGB `.npy` images pass through unchanged. Video, PNG/JPEG, undistortion and resizing need separate preprocessing dependencies:

```sh
python3 -m venv /path/to/preprocessing-env
/path/to/preprocessing-env/bin/pip install --require-hashes --only-binary=:all: \
  -r ml/rangle_preprocessing/requirements.lock
```

The Linux x86_64 / CPython 3.11–3.14 lock pins NumPy 2.4.2 and OpenCV headless 4.13.0.92 with official PyPI wheel hashes, independently of the trainer image/lock.

## Source manifest v1

Required top-level fields: `version`, `adapter`, `source`, `frames`, `output`, `seed`, `normalization`, and `views` or `vru`. Optional `checksums` maps relative paths to lowercase SHA256. Validation rejects unknown fields, duplicate JSON keys, nonfinite numbers, unused checksum paths and checksum mismatches.

| Field | Contract |
| --- | --- |
| `source` | Nonempty `name`, public HTTPS `url`, `license`, `attribution`, `calibration_license`, `initialization_scope`. Record separate calibration/seed terms and whether held-out cameras contributed to geometry/calibration, including when unknown. Statements are caller-supplied; adapter/URL imply no legal permission or public/cloud approval. |
| `frames` | `indices`: 1–32 uniformly spaced source indices; `fps`: synchronized source rate ≤240 fps. One frame produces static NPZ, multiple frames temporal NPZ. Time is `(index - first_index) / source_fps`; stride 2 halves output fps. Videos decode sequentially from frame zero; image lists correspond to selected indices. Views must share indices/fps; no clock-offset estimation. |
| `output` | Target `width`/`height` within worker bounds. |
| `seed` | Relative `path`, explicit `max_points` 100–1,000,000 and integer `seed`. Larger clouds are sampled deterministically; source/selected counts are recorded. |
| `normalization` | `training-camera-centres`: training camera mean and maximum distance define centre/scale, applied to seed and all translations. `none`: centre zero, scale one. Held-out cameras do not choose normalization but may contribute to supplied geometry/calibration. |

Seed formats: NPZ with exactly finite float `xyz [N,3]` and uint8 `rgb [N,3]` (bounded headers, no pickle), or vertex-only ASCII/binary little-endian PLY with float `x/y/z` and uint8 `red/green/blue`. Extra scalar properties are ignored; faces, lists, unsupported types and inconsistent sizes fail.

## Generic calibrated adapter

`adapter: calibrated-manifest` requires 4–64 views, unique string/integer IDs, explicit `train`/`heldout` splits, at least three training and one held-out camera. List order defines array indices. A view is:

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

Use `media.video` for video; image-list length must match frame count. NPY is uint8 RGB `[height,width,3]`; PNG/JPEG must be 8-bit RGB. Alpha PNG is rejected. Transforms follow [COLMAP/OpenCV world-to-camera](https://colmap.github.io/format.html), positive Z forward (Hamilton `w,x,y,z` for derived quaternions); the generic adapter takes the explicit rigid matrix.

`K` uses original dimensions. Optional `distortion` accepts OpenCV 4/5/8-coefficient pinhole models; optional `rectified` supplies target `K`, `width`, `height`. Otherwise source `K` is used. Intrinsics scale independently in x/y; [initUndistortRectifyMap](https://docs.opencv.org/4.13.0/d9/d0c/group__calib3d.html) and linear remapping convert pixels. No intrinsics optimization or additional unrequested undistortion occurs.

Wrap the views in the shared fields:

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

Replace placeholders and the intentionally invalid empty `views` with actual source terms and calibrated records, including held-out splits. Another dataset's license does not apply automatically.

## VRU COLMAP adapter

Set `adapter: vru-colmap`; replace `views` with:

```json
{
  "vru": {
    "videos": "raw/videos",
    "calibration": "calibration",
    "heldout_view_ids": [0, 10, 20, 30]
  }
}
```

The adapter reads `frame000000/distorted/sparse/0/cameras.bin` (COLMAP OPENCV model 4), `frame000000/sparse/0/cameras.txt` (PINHOLE intrinsics) and `frame000000/sparse/0/images.txt` (`camN` poses). It sorts numeric IDs and resolves `videos/train/N.mp4` / `videos/test/N.mp4` using the explicit split. `seed.path` stays explicit.

The [VRU Basketball dataset card](https://huggingface.co/datasets/BestWJH/VRU_Basketball) labels raw data CC-BY-4.0 and documents standard held-out IDs. Record calibration/seed provenance separately; author-linked archives do not imply matching licenses.

## Output, validation, and equivalence

The worker validates the existing seven-key static/eight-key temporal NPZ before publication: camera geometry, finite bounded arrays, disjoint splits, times, temporal images ≤512 MiB, ZIP expansion ≤1 GiB and compressed download ≤256 MiB. Static limits are unchanged. Source NPY/seed headers are inspected before allocation; media dimensions must match calibration.

Sidecar `rangle-prepared-dataset-v1` retains source/license/attribution, manifest SHA, relative input paths/roles/SHA/bytes, view IDs/splits, source/output frames/times/fps, normalization, seed counts, array and NPZ SHA/bytes, dependency versions and initialization caveats. Keep source media, NPZ and private manifests outside Git.

Verified preprocessing cases:

- Approved VRU `gz`: 34 videos, supplied COLMAP calibration and colored PLY regenerated the earlier NPZ byte-for-byte: **20×34×270×480 RGB; 171,336,472B; SHA256 `f735fbbdfd9b17846941d7fdede7a2617999fc77a06cb0f6dc906000a7e55c53`**. All eight arrays and static frame zero matched; seed 0, 99,330 points, 30 train/4 held-out, normalization and `0,0.04,…,0.76` times were retained.
- Second cached, research-restricted sequence: generic conversion from calibrated RGB derivatives, K/poses and untrained PLY passed on local CPU. Original RGBA is rejected. Data/outputs/metadata stayed private; this demonstrates conversion coverage, not training quality on another sequence.

NumPy-only contracts cover conversion, checksums/path bounds, schemas, projection invariance, frame stride, seed sampling, COLMAP, provenance and overwrite refusal:

```sh
PYTHONPATH=ml python3 -m unittest discover -s ml/tests -v
ruff check ml/rangle_preprocessing ml/rangle_training ml/tests
```

The explicit media profile requires preprocessing dependencies (missing dependencies fail) and tests PNG/video RGB decoding, exact frame selection, synchronized fps and resized intrinsics:

```sh
PYTHONPATH=ml:ml/tests python3 -m unittest discover -s ml/rangle_preprocessing/tests -v
```
