"""Validate the checksummed, calibrated NumPy input contract without pickle."""

import math
import zipfile
from dataclasses import dataclass
from pathlib import Path

import numpy as np

STATIC_KEYS = {
    "images",
    "viewmats",
    "Ks",
    "xyz",
    "rgb",
    "train_indices",
    "heldout_indices",
}
MAX_TEMPORAL_IMAGE_BYTES = 512 << 20


@dataclass(frozen=True)
class Dataset:
    images: np.ndarray
    viewmats: np.ndarray
    Ks: np.ndarray
    xyz: np.ndarray
    rgb: np.ndarray
    train_indices: np.ndarray
    heldout_indices: np.ndarray
    frame_times: np.ndarray | None = None

    @property
    def frame_rate(self) -> float:
        return 1.0 if self.frame_times is None else float(1 / self.frame_times[1])


def _validate_archive(path, mode):
    """Check actual NPY headers before NumPy can allocate from an unsafe shape."""
    keys = STATIC_KEYS | ({"frame_times"} if mode == "dynamic" else set())
    headers = {}
    with zipfile.ZipFile(path) as archive:
        members = archive.infolist()
        if (
            {entry.filename for entry in members} != {key + ".npy" for key in keys}
            or len(members) != len(keys)
            or sum(entry.file_size for entry in members) > 1 << 30
        ):
            raise ValueError("Invalid or oversized dataset archive")
        for entry in members:
            if entry.flag_bits & 1 or entry.compress_type not in (
                zipfile.ZIP_STORED,
                zipfile.ZIP_DEFLATED,
            ):
                raise ValueError("Unsupported dataset archive member")
            with archive.open(entry) as member:
                version = np.lib.format.read_magic(member)
                if version == (1, 0):
                    shape, _, dtype = np.lib.format.read_array_header_1_0(
                        member, max_header_size=4096
                    )
                elif version == (2, 0):
                    shape, _, dtype = np.lib.format.read_array_header_2_0(
                        member, max_header_size=4096
                    )
                else:
                    raise ValueError("Unsupported NumPy array version")
                if (
                    dtype.hasobject
                    or dtype.fields is not None
                    or dtype.kind not in "uif"
                    or member.tell() + math.prod(shape) * dtype.itemsize
                    != entry.file_size
                ):
                    raise ValueError("Invalid NumPy array dtype or declared size")
                headers[entry.filename[:-4]] = (shape, dtype)
    shape, dtype = headers["images"]
    dimensions = 5 if mode == "dynamic" else 4
    if dtype != np.uint8 or len(shape) != dimensions or shape[-1] != 3:
        raise ValueError(
            f"images must be uint8 with {dimensions} dimensions ending in RGB"
        )
    views, height, width, _ = shape[-4:]
    if not (4 <= views <= 64 and 64 <= height <= 1080 and 64 <= width <= 1920):
        raise ValueError("Dataset image budget exceeded")
    if mode == "dynamic":
        if not 2 <= shape[0] <= 32 or math.prod(shape) > MAX_TEMPORAL_IMAGE_BYTES:
            raise ValueError("Temporal dataset image budget exceeded")
        if headers["frame_times"] != ((shape[0],), np.dtype("float64")):
            raise ValueError("frame_times must be float64 [frames]")
    elif math.prod(shape) > 256_000_000:
        raise ValueError("Dataset image budget exceeded")
    # Reject oversized geometry/splits before allocating them as well.
    xyz_shape, _ = headers["xyz"]
    if len(xyz_shape) != 2 or xyz_shape[1] != 3 or not 100 <= xyz_shape[0] <= 1_000_000:
        raise ValueError("xyz must contain 100 to 1,000,000 seed points")
    expected = {"viewmats": (views, 4, 4), "Ks": (views, 3, 3), "rgb": xyz_shape}
    if any(headers[key][0] != shape for key, shape in expected.items()):
        raise ValueError("Invalid camera or seed color shape")
    if any(
        len(headers[key][0]) != 1 or not 1 <= headers[key][0][0] <= views
        for key in ("train_indices", "heldout_indices")
    ):
        raise ValueError("Invalid training/evaluation camera split")
    return keys


def load_dataset(path: Path, mode="static") -> Dataset:
    if mode not in ("static", "dynamic"):
        raise ValueError("Invalid training mode")
    keys = _validate_archive(path, mode)
    with np.load(path, allow_pickle=False, max_header_size=4096) as arrays:
        data = Dataset(**{key: arrays[key] for key in keys})
    views = data.images.shape[-4]
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
    if data.frame_times is not None:
        times = data.frame_times
        intervals = np.diff(times)
        if (
            not np.isfinite(times).all()
            or times[0] != 0
            or (intervals <= 0).any()
            or times[-1] > 1e6
            or not np.allclose(intervals, intervals[0], rtol=1e-8, atol=1e-10)
            or not 0 < 1 / intervals[0] <= 240
        ):
            raise ValueError(
                "frame_times must be uniform finite seconds from zero, at most 240 fps"
            )
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
    height, width = data.images.shape[-3:-1]
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


def training_activity(data: Dataset) -> dict[int, np.ndarray]:
    """Fixed temporal RGB variation maps from training cameras only.

    Welford accumulation avoids materializing a float copy of the whole video.
    A fixed 0.05 RGB standard deviation maps to activity one. This is an
    appearance-change heuristic, not foreground supervision or optical flow.
    """
    if data.frame_times is None:
        raise ValueError("Temporal activity requires a video dataset")
    result = {}
    for index in data.train_indices:
        mean = np.zeros(data.images.shape[-3:], np.float32)
        second = np.zeros_like(mean)
        for frame, pixels in enumerate(data.images[:, int(index)]):
            value = pixels.astype(np.float32) / 255
            delta = value - mean
            mean += delta / (frame + 1)
            second += delta * (value - mean)
        variance = np.maximum(second / len(data.frame_times), 0)
        result[int(index)] = np.clip(np.sqrt(variance.mean(axis=-1)) / 0.05, 0, 1)
    return result
