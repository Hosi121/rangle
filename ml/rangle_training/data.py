"""Validate the checksummed, calibrated NumPy input contract without pickle."""

import zipfile
from dataclasses import dataclass
from pathlib import Path

import numpy as np


@dataclass(frozen=True)
class Dataset:
    images: np.ndarray
    viewmats: np.ndarray
    Ks: np.ndarray
    xyz: np.ndarray
    rgb: np.ndarray
    train_indices: np.ndarray
    heldout_indices: np.ndarray


def load_dataset(path: Path) -> Dataset:
    keys = set(Dataset.__dataclass_fields__)
    with zipfile.ZipFile(path) as archive:
        members = archive.infolist()
        if (
            {entry.filename for entry in members} != {key + ".npy" for key in keys}
            or len(members) != len(keys)
            or sum(entry.file_size for entry in members) > 1 << 30
        ):
            raise ValueError("Invalid or oversized dataset archive")
    with np.load(path, allow_pickle=False, max_header_size=4096) as arrays:
        data = Dataset(**{key: arrays[key] for key in keys})
    images = data.images
    if images.dtype != np.uint8 or images.ndim != 4 or images.shape[-1] != 3:
        raise ValueError("images must be uint8 [views, height, width, 3]")
    views, height, width, _ = images.shape
    if not (
        4 <= views <= 64
        and 64 <= height <= 1080
        and 64 <= width <= 1920
        and images.size <= 256_000_000
    ):
        raise ValueError("Dataset image budget exceeded")
    if data.viewmats.shape != (views, 4, 4) or data.Ks.shape != (views, 3, 3):
        raise ValueError("One calibrated OpenCV camera is required per image")
    if (
        data.xyz.ndim != 2
        or data.xyz.shape[1] != 3
        or not 100 <= len(data.xyz) <= 1_000_000
    ):
        raise ValueError("xyz must contain 100 to 1,000,000 seed points")
    if data.rgb.dtype != np.uint8 or data.rgb.shape != data.xyz.shape:
        raise ValueError("rgb must be uint8 and match the seed points")
    for array in (data.viewmats, data.Ks, data.xyz):
        if (
            array.dtype.kind != "f"
            or not np.isfinite(array).all()
            or np.abs(array).max() > 1e6
        ):
            raise ValueError("Geometry must contain finite bounded floats")
    rotations = data.viewmats[:, :3, :3]
    if (
        not np.allclose(rotations @ rotations.transpose(0, 2, 1), np.eye(3), atol=1e-3)
        or not np.allclose(np.linalg.det(rotations), 1, atol=1e-3)
        or not np.allclose(data.viewmats[:, 3], [0, 0, 0, 1], atol=1e-5)
        or not np.allclose(data.Ks[:, 2], [0, 0, 1], atol=1e-5)
        or (data.Ks[:, [0, 1], [0, 1]] <= 0).any()
    ):
        raise ValueError("Invalid rigid camera transform or intrinsics")
    for indices in (data.train_indices, data.heldout_indices):
        if (
            indices.dtype.kind not in "iu"
            or indices.ndim != 1
            or len(indices) == 0
            or len(np.unique(indices)) != len(indices)
            or (indices >= views).any()
            or (indices < 0).any()
        ):
            raise ValueError("Invalid training/evaluation camera split")
    if (
        len(data.train_indices) < 3
        or np.intersect1d(data.train_indices, data.heldout_indices).size
    ):
        raise ValueError("Training and evaluation views must be disjoint")
    return data


def scene_camera(data: Dataset) -> dict:
    index = int(data.train_indices[0])
    view = data.viewmats[index].astype(np.float64)
    rotation = view[:3, :3]
    position = -rotation.T @ view[:3, 3]
    forward = rotation.T @ np.array([0.0, 0.0, 1.0])
    up = rotation.T @ np.array([0.0, -1.0, 0.0])
    depths = (data.xyz - position) @ forward
    positive = depths[depths > 0]
    distance = max(float(np.median(positive)) if positive.size else 1.0, 0.1)
    height, width = data.images.shape[1:3]
    K = data.Ks[index]
    return {
        "position": position.tolist(),
        "lookAt": (position + distance * forward).tolist(),
        "up": up.tolist(),
        "fovYDegrees": float(np.degrees(2 * np.arctan(height / (2 * K[1, 1])))),
        "referenceImageHeight": height,
        "intrinsics": {
            "width": width,
            "height": height,
            "fx": float(K[0, 0]),
            "fy": float(K[1, 1]),
            "cx": float(K[0, 2]),
            "cy": float(K[1, 2]),
        },
    }
