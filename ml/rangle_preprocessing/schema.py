"""Versioned local-source manifest validation and camera normalization."""

import hashlib
import json
import math
from pathlib import Path
from urllib.parse import urlsplit

import numpy as np

SOURCE_FIELDS = {
    "name",
    "url",
    "license",
    "attribution",
    "calibration_license",
    "initialization_scope",
}


def exact_keys(value, required, optional=()):
    if (
        not isinstance(value, dict)
        or not required <= value.keys()
        or value.keys() - required - set(optional)
    ):
        raise ValueError(
            f"Expected keys {sorted(required)} and optional {sorted(optional)}"
        )


def integer(value, minimum, maximum, label):
    if (
        isinstance(value, bool)
        or not isinstance(value, int)
        or not minimum <= value <= maximum
    ):
        raise ValueError(f"Invalid {label}")
    return value


def _pairs(items):
    result = {}
    for key, value in items:
        if key in result:
            raise ValueError("Duplicate manifest key")
        result[key] = value
    return result


def _reject_constant(_):
    raise ValueError("Nonfinite manifest number")


def read_manifest(path, *, include_checksum=False):
    path = Path(path)
    with path.open("rb") as stream:
        payload = stream.read((2 << 20) + 1)
    if len(payload) > 2 << 20:
        raise ValueError("Manifest exceeds 2 MiB")
    value = json.loads(
        payload,
        object_pairs_hook=_pairs,
        parse_constant=_reject_constant,
    )
    exact_keys(
        value,
        {"version", "adapter", "source", "frames", "output", "seed", "normalization"},
        {"views", "vru", "checksums"},
    )
    if (
        not isinstance(value["version"], int)
        or isinstance(value["version"], bool)
        or value["version"] != 1
    ):
        raise ValueError("Unsupported source manifest version")
    if value["adapter"] not in ("calibrated-manifest", "vru-colmap"):
        raise ValueError("Unsupported source adapter")
    expected = "views" if value["adapter"] == "calibrated-manifest" else "vru"
    if expected not in value or ({"views", "vru"} - {expected}) & value.keys():
        raise ValueError("Source adapter and payload differ")
    exact_keys(value["source"], SOURCE_FIELDS)
    if any(
        not isinstance(text, str) or not text.strip() or len(text) > 4000
        for text in value["source"].values()
    ):
        raise ValueError(
            "Source, licenses, attribution and initialization scope are required"
        )
    url = urlsplit(value["source"]["url"])
    if url.scheme != "https" or not url.netloc or url.username or url.password:
        raise ValueError("Source URL must be an explicit public HTTPS reference")
    exact_keys(value["frames"], {"indices", "fps"})
    frames = value["frames"]["indices"]
    if not isinstance(frames, list) or not 1 <= len(frames) <= 32:
        raise ValueError("Select 1 to 32 source frames")
    for frame in frames:
        integer(frame, 0, 1_000_000, "source frame")
    intervals = np.diff(frames)
    if len(intervals) and (
        (intervals <= 0).any() or not (intervals == intervals[0]).all()
    ):
        raise ValueError("Source frames must increase with a uniform stride")
    fps = value["frames"]["fps"]
    if (
        isinstance(fps, bool)
        or not isinstance(fps, (int, float))
        or not math.isfinite(fps)
        or not 0 < fps <= 240
    ):
        raise ValueError("Source fps must be finite and at most 240")
    exact_keys(value["output"], {"width", "height"})
    integer(value["output"]["width"], 64, 1920, "output width")
    integer(value["output"]["height"], 64, 1080, "output height")
    exact_keys(value["seed"], {"path", "max_points", "seed"})
    integer(value["seed"]["max_points"], 100, 1_000_000, "maximum seed points")
    integer(value["seed"]["seed"], 0, 2**32 - 1, "seed")
    if value["normalization"] not in ("training-camera-centres", "none"):
        raise ValueError("Unsupported normalization")
    checksums = value.get("checksums", {})
    if not isinstance(checksums, dict) or any(
        not isinstance(key, str)
        or not isinstance(digest, str)
        or len(digest) != 64
        or any(char not in "0123456789abcdef" for char in digest)
        for key, digest in checksums.items()
    ):
        raise ValueError("Expected lowercase SHA256 checksums indexed by source path")
    return (value, hashlib.sha256(payload).hexdigest()) if include_checksum else value


def local_path(root, value):
    if (
        not isinstance(value, str)
        or not value
        or Path(value).is_absolute()
        or ".." in Path(value).parts
    ):
        raise ValueError(
            "Source paths must be relative and stay inside the source root"
        )
    result = (root / value).resolve()
    if not result.is_relative_to(root) or not result.is_file():
        raise ValueError("Source path is missing or leaves the source root")
    return result


def matrix(value, shape, label):
    try:
        result = np.asarray(value, dtype=np.float64)
    except (TypeError, ValueError) as error:
        raise ValueError(f"Invalid {label}") from error
    if (
        result.shape != shape
        or not np.isfinite(result).all()
        or np.abs(result).max() > 1e6
    ):
        raise ValueError(f"Expected finite bounded {label} with shape {shape}")
    return result


def intrinsics(value):
    result = matrix(value, (3, 3), "pinhole intrinsics")
    if (
        (result[[0, 1], [0, 1]] <= 0).any()
        or not np.allclose(result[2], [0, 0, 1], atol=1e-10)
        or abs(result[0, 1]) > 1e-10
        or abs(result[1, 0]) > 1e-10
    ):
        raise ValueError("Unsupported pinhole intrinsics")
    return result


def calibration(value):
    exact_keys(
        value, {"width", "height", "K", "world_to_camera"}, {"distortion", "rectified"}
    )
    integer(value["width"], 1, 8192, "source width")
    integer(value["height"], 1, 8192, "source height")
    if value["width"] * value["height"] > 64_000_000:
        raise ValueError("Source image pixel budget exceeded")
    K = intrinsics(value["K"])
    view = matrix(value["world_to_camera"], (4, 4), "OpenCV world-to-camera transform")
    rotation = view[:3, :3]
    if (
        not np.allclose(rotation @ rotation.T, np.eye(3), atol=1e-6)
        or not np.isclose(np.linalg.det(rotation), 1, atol=1e-6)
        or not np.allclose(view[3], [0, 0, 0, 1], atol=1e-10)
    ):
        raise ValueError("Invalid rigid camera transform")
    distortion = np.asarray(value.get("distortion", []), dtype=np.float64)
    if (
        distortion.ndim != 1
        or len(distortion) not in (0, 4, 5, 8)
        or not np.isfinite(distortion).all()
        or (np.abs(distortion) > 1e6).any()
    ):
        raise ValueError("Unsupported OpenCV distortion")
    rectified = value.get(
        "rectified", {"width": value["width"], "height": value["height"], "K": K}
    )
    exact_keys(rectified, {"width", "height", "K"})
    integer(rectified["width"], 1, 8192, "rectified width")
    integer(rectified["height"], 1, 8192, "rectified height")
    rectified_K = intrinsics(rectified["K"])
    return {
        "width": value["width"],
        "height": value["height"],
        "K": K,
        "world_to_camera": view,
        "distortion": distortion,
        "rectified": {**rectified, "K": rectified_K},
    }


def validate_views(views, config):
    if not isinstance(views, list) or not 4 <= len(views) <= 64:
        raise ValueError("Expected 4 to 64 calibrated views")
    result, ids = [], set()
    frames = len(config["frames"]["indices"])
    output = config["output"]
    limit = (512 << 20) if frames > 1 else 256_000_000
    if frames * len(views) * output["height"] * output["width"] * 3 > limit:
        raise ValueError("Output image budget exceeded")
    for view in views:
        exact_keys(view, {"id", "split", "media", "calibration"})
        view_id = view["id"]
        if (
            isinstance(view_id, bool)
            or not isinstance(view_id, (str, int))
            or not str(view_id)
            or len(str(view_id)) > 64
            or str(view_id) in ids
        ):
            raise ValueError("Invalid or duplicate view ID")
        ids.add(str(view_id))
        if view["split"] not in ("train", "heldout"):
            raise ValueError("View split must be train or heldout")
        media = view["media"]
        if not isinstance(media, dict) or set(media) not in ({"video"}, {"images"}):
            raise ValueError("Each view needs one video or an image list")
        if "images" in media and (
            not isinstance(media["images"], list) or len(media["images"]) != frames
        ):
            raise ValueError("Image list must match the selected frame count")
        paths = media.get("images", [media.get("video")])
        if any(not isinstance(name, str) or not name for name in paths):
            raise ValueError("Media paths must be nonempty strings")
        result.append({**view, "calibration": calibration(view["calibration"])})
    if sum(view["split"] == "train" for view in result) < 3 or not any(
        view["split"] == "heldout" for view in result
    ):
        raise ValueError("At least 3 training and 1 held-out cameras are required")
    return result


def normalize_geometry(xyz, views, train, method):
    views = views.copy()
    if method == "none":
        centre, scale = np.zeros(3), 1.0
    else:
        centres = np.stack([-view[:3, :3].T @ view[:3, 3] for view in views])
        centre = centres[train].mean(axis=0)
        scale = float(np.linalg.norm(centres[train] - centre, axis=1).max())
        if not np.isfinite(scale) or scale <= 0:
            raise ValueError(
                "Training camera centres must define a positive normalization scale"
            )
    xyz = (xyz - centre) / scale
    for view in views:
        view[:3, 3] = (view[:3, :3] @ centre + view[:3, 3]) / scale
    return (
        xyz.astype(np.float32),
        views.astype(np.float32),
        {
            "centre": centre.tolist(),
            "scale": scale,
            "derived_from": method,
        },
    )
