"""VRU layout adapter; only documented COLMAP OPENCV/PINHOLE fields are read."""

import re
import struct
from pathlib import PurePosixPath

import numpy as np

from .schema import exact_keys, integer


def _lines(path):
    if path.stat().st_size > 64 << 20:
        raise ValueError("Calibration text exceeds budget")
    return [
        line.strip()
        for line in path.read_text().splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    ]


def _read_exact(stream, size):
    data = stream.read(size)
    if len(data) != size:
        raise ValueError("Truncated COLMAP camera file")
    return data


def _distorted_cameras(path):
    cameras = {}
    with path.open("rb") as stream:
        (count,) = struct.unpack("<Q", _read_exact(stream, 8))
        if not 1 <= count <= 1000:
            raise ValueError("Invalid COLMAP camera count")
        for _ in range(count):
            camera_id, model, width, height = struct.unpack(
                "<iiQQ", _read_exact(stream, 24)
            )
            if model != 4 or camera_id in cameras:
                raise ValueError("VRU adapter requires unique COLMAP OPENCV cameras")
            fx, fy, cx, cy, *distortion = struct.unpack("<8d", _read_exact(stream, 64))
            cameras[camera_id] = {
                "width": width,
                "height": height,
                "K": [[fx, 0, cx], [0, fy, cy], [0, 0, 1]],
                "distortion": distortion,
            }
        if stream.read(1):
            raise ValueError("Trailing COLMAP camera bytes")
    return cameras


def _pinhole_cameras(path):
    cameras = {}
    for line in _lines(path):
        fields = line.split()
        if len(fields) != 8 or fields[1] != "PINHOLE":
            raise ValueError("VRU adapter requires supplied PINHOLE calibration")
        camera_id = int(fields[0])
        if camera_id in cameras:
            raise ValueError("Duplicate pinhole camera ID")
        width, height = map(int, fields[2:4])
        fx, fy, cx, cy = map(float, fields[4:])
        cameras[camera_id] = {
            "width": width,
            "height": height,
            "K": [[fx, 0, cx], [0, fy, cy], [0, 0, 1]],
        }
    return cameras


def _rotation(values):
    q = np.asarray(values, np.float64)
    norm = np.linalg.norm(q)
    if not np.isfinite(q).all() or norm < 1e-12:
        raise ValueError("Invalid camera quaternion")
    w, x, y, z = q / norm
    return np.array(
        [
            [1 - 2 * (y * y + z * z), 2 * (x * y - z * w), 2 * (x * z + y * w)],
            [2 * (x * y + z * w), 1 - 2 * (x * x + z * z), 2 * (y * z - x * w)],
            [2 * (x * z - y * w), 2 * (y * z + x * w), 1 - 2 * (x * x + y * y)],
        ]
    )


def _poses(path):
    poses = []
    for line in _lines(path):
        fields = line.split()
        # COLMAP observations contain triples; only an image header has 10 fields.
        if len(fields) != 10:
            if len(fields) % 3:
                raise ValueError("Invalid COLMAP observation row")
            continue
        _, qw, qx, qy, qz, tx, ty, tz, camera_id, name = fields
        match = re.fullmatch(r"cam(\d+)", PurePosixPath(name).stem)
        if match is None:
            raise ValueError("VRU image names must identify camN")
        view = np.eye(4)
        view[:3, :3] = _rotation(list(map(float, (qw, qx, qy, qz))))
        view[:3, 3] = list(map(float, (tx, ty, tz)))
        poses.append((int(match[1]), int(camera_id), view))
    if not poses or len({pose[0] for pose in poses}) != len(poses):
        raise ValueError("Missing or repeated VRU camera poses")
    return sorted(poses, key=lambda pose: pose[0])


def adapt_vru(config, files):
    value = config["vru"]
    exact_keys(value, {"videos", "calibration", "heldout_view_ids"})
    if not isinstance(value["heldout_view_ids"], list) or not value["heldout_view_ids"]:
        raise ValueError("VRU heldout view IDs must be explicit")
    for view_id in value["heldout_view_ids"]:
        integer(view_id, 0, 1_000_000, "heldout view ID")
    if len(set(value["heldout_view_ids"])) != len(value["heldout_view_ids"]):
        raise ValueError("Duplicate heldout view IDs")
    if any(
        not isinstance(value[key], str) or not value[key]
        for key in ("videos", "calibration")
    ):
        raise ValueError(
            "VRU video/calibration paths must be nonempty relative strings"
        )
    base = PurePosixPath(value["calibration"])
    distorted = _distorted_cameras(
        files.get(
            str(base / "frame000000/distorted/sparse/0/cameras.bin"),
            "distorted-calibration",
        )
    )
    pinhole = _pinhole_cameras(
        files.get(
            str(base / "frame000000/sparse/0/cameras.txt"), "rectified-calibration"
        )
    )
    poses = _poses(
        files.get(str(base / "frame000000/sparse/0/images.txt"), "camera-poses")
    )
    if not set(value["heldout_view_ids"]) <= {pose[0] for pose in poses}:
        raise ValueError("Requested heldout VRU camera has no supplied pose")
    views = []
    for view_id, camera_id, view in poses:
        if camera_id not in distorted or camera_id not in pinhole:
            raise ValueError("Pose has no matching distorted/pinhole calibration")
        split = "heldout" if view_id in value["heldout_view_ids"] else "train"
        relative = (
            PurePosixPath(value["videos"])
            / ("test" if split == "heldout" else "train")
            / f"{view_id}.mp4"
        )
        views.append(
            {
                "id": view_id,
                "split": split,
                "media": {"video": str(relative)},
                "calibration": {
                    **distorted[camera_id],
                    "rectified": pinhole[camera_id],
                    "world_to_camera": view.tolist(),
                },
            }
        )
    return views
