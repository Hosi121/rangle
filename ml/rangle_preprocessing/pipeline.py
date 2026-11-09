"""Shared adapter-to-NPZ pipeline with provenance and immutable local output."""

import hashlib
import importlib.metadata
import json
import os
import tempfile
from pathlib import Path

import numpy as np
from rangle_training.data import load_dataset

from .schema import normalize_geometry, read_manifest, validate_views
from .sources import SourceFiles, decode_view, load_seed
from .vru import adapt_vru


def prepare_dataset(manifest_path, output_path, *, source_root=None):
    manifest_path, output_path = Path(manifest_path), Path(output_path)
    metadata_path = output_path.with_suffix(".json")
    if output_path.suffix != ".npz" or output_path == metadata_path:
        raise ValueError("Output must use the .npz extension")
    if (
        output_path.exists()
        or output_path.is_symlink()
        or metadata_path.exists()
        or metadata_path.is_symlink()
    ):
        raise FileExistsError("Refusing to overwrite prepared input or metadata")
    config, manifest_sha256 = read_manifest(manifest_path, include_checksum=True)
    root = Path(source_root or manifest_path.parent).resolve(strict=True)
    if not root.is_dir():
        raise ValueError("Source root must be a local directory")
    files = SourceFiles(root, config.get("checksums", {}))
    views = validate_views(
        config.get("views")
        if config["adapter"] == "calibrated-manifest"
        else adapt_vru(config, files),
        config,
    )
    seed_path = files.get(config["seed"]["path"], "seed-cloud")
    xyz, rgb, source_points = load_seed(
        seed_path, config["seed"]["max_points"], config["seed"]["seed"]
    )
    decoded, matrices, Ks = [], [], []
    train, heldout = [], []
    for index, view in enumerate(views):
        images, K = decode_view(view, config["frames"], config["output"], files)
        decoded.append(images)
        matrices.append(view["calibration"]["world_to_camera"])
        Ks.append(K)
        (train if view["split"] == "train" else heldout).append(index)
    if files.checked != set(files.expected):
        raise ValueError("Manifest contains unused source checksum paths")
    matrices = np.stack(matrices)
    xyz, matrices, normalization = normalize_geometry(
        xyz, matrices, train, config["normalization"]
    )
    images = np.stack(decoded, axis=1)
    arrays = {
        "images": images if len(images) > 1 else images[0],
        "viewmats": matrices,
        "Ks": np.stack(Ks).astype(np.float32),
        "xyz": xyz,
        "rgb": rgb.astype(np.uint8),
        "train_indices": np.array(train, np.int64),
        "heldout_indices": np.array(heldout, np.int64),
    }
    frames = config["frames"]["indices"]
    if len(frames) > 1:
        arrays["frame_times"] = (np.asarray(frames, np.float64) - frames[0]) / config[
            "frames"
        ]["fps"]
    dependencies = {"numpy": np.__version__}
    for package in ("opencv-python-headless", "opencv-python"):
        try:
            dependencies[package] = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError:
            pass
    metadata = {
        "version": 1,
        "schema": "rangle-prepared-dataset-v1",
        "adapter": config["adapter"],
        "source": config["source"],
        "source_manifest_sha256": manifest_sha256,
        "source_files": list(files.records.values()),
        "view_ids": [view["id"] for view in views],
        "source_frames": frames,
        "frame_count": len(frames),
        "output": config["output"],
        "source_fps": config["frames"]["fps"],
        "frame_times_seconds": arrays.get("frame_times", np.zeros(1)).tolist(),
        "mode": "dynamic" if len(frames) > 1 else "static",
        "images_shape": list(arrays["images"].shape),
        "image_bytes": arrays["images"].nbytes,
        "seed": config["seed"]["seed"],
        "source_points": source_points,
        "points": len(xyz),
        "train_indices": train,
        "heldout_indices": heldout,
        "normalization": normalization,
        "camera_convention": "opencv_world_to_camera",
        "resampling": "OpenCV OPENCV distortion to explicit PINHOLE intrinsics, scaled independently x/y; linear remap; identity RGB passes through",
        "heldout_scope": "Pixels excluded from training; source calibration/seed contribution is recorded in source.initialization_scope",
        "array_sha256": {
            key: hashlib.sha256(np.ascontiguousarray(value)).hexdigest()
            for key, value in arrays.items()
        },
        "dependencies": dependencies,
    }
    output_path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(
        prefix=".rangle-prepare-", dir=output_path.parent
    ) as temporary:
        temporary = Path(temporary)
        dataset_file = temporary / "dataset.npz"
        np.savez_compressed(dataset_file, **arrays)
        if dataset_file.stat().st_size > 256 << 20:
            raise ValueError("Prepared download exceeds 256 MiB")
        validated = load_dataset(dataset_file, mode=metadata["mode"])
        metadata.update(
            frame_rate=validated.frame_rate, npz_bytes=dataset_file.stat().st_size
        )
        with dataset_file.open("rb") as stream:
            metadata["npz_sha256"] = hashlib.file_digest(stream, "sha256").hexdigest()
        metadata_file = temporary / "metadata.json"
        metadata_file.write_text(json.dumps(metadata, indent=2, allow_nan=False) + "\n")
        # Same-filesystem hard links publish complete files without replacing any existing path.
        os.link(dataset_file, output_path)
        try:
            os.link(metadata_file, metadata_path)
        except BaseException:
            if output_path.stat().st_ino == dataset_file.stat().st_ino:
                output_path.unlink()
            raise
    return metadata
