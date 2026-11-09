"""Bounded source files, seed clouds, and explicit image/video decoding."""

import hashlib
import math
import struct
import zipfile

import numpy as np

from .schema import local_path


class SourceFiles:
    def __init__(self, root, expected):
        self.root, self.expected, self.records = root, expected, {}
        self.checked = set()

    def get(self, name, role):
        path = local_path(self.root, name)
        key = path.relative_to(self.root).as_posix()
        if key not in self.records:
            with path.open("rb") as stream:
                digest = hashlib.file_digest(stream, "sha256").hexdigest()

            self.records[key] = {
                "path": key,
                "sha256": digest,
                "bytes": path.stat().st_size,
                "roles": [],
            }
        if name in self.expected:
            if self.records[key]["sha256"] != self.expected[name]:
                raise ValueError(f"Source checksum mismatch: {name}")
            self.checked.add(name)
        if role not in self.records[key]["roles"]:
            self.records[key]["roles"].append(role)
        return path


def _npy_header(stream, size, budget):
    version = np.lib.format.read_magic(stream)
    if version == (1, 0):
        shape, _, dtype = np.lib.format.read_array_header_1_0(
            stream, max_header_size=4096
        )
    elif version == (2, 0):
        shape, _, dtype = np.lib.format.read_array_header_2_0(
            stream, max_header_size=4096
        )
    else:
        raise ValueError("Unsupported NumPy source version")
    byte_count = math.prod(shape) * dtype.itemsize
    if (
        dtype.hasobject
        or dtype.fields is not None
        or dtype.kind not in "uif"
        or byte_count > budget
        or stream.tell() + byte_count != size
    ):
        raise ValueError("Unsafe or oversized NumPy source")
    return shape, dtype


def _seed_npz(path):
    if path.stat().st_size > 64 << 20:
        raise ValueError("Seed NPZ exceeds file budget")
    with zipfile.ZipFile(path) as archive:
        entries = archive.infolist()
        if (
            len(entries) != 2
            or {entry.filename for entry in entries} != {"xyz.npy", "rgb.npy"}
            or sum(entry.file_size for entry in entries) > 64 << 20
        ):
            raise ValueError("Seed NPZ must contain only bounded xyz/rgb arrays")
        headers = {}
        for entry in entries:
            if entry.flag_bits & 1 or entry.compress_type not in (
                zipfile.ZIP_STORED,
                zipfile.ZIP_DEFLATED,
            ):
                raise ValueError("Unsupported seed archive member")
            with archive.open(entry) as stream:
                headers[entry.filename] = _npy_header(stream, entry.file_size, 32 << 20)
        shape, dtype = headers["xyz.npy"]
        if (
            len(shape) != 2
            or shape[1] != 3
            or not 100 <= shape[0] <= 1_000_000
            or dtype.kind != "f"
            or headers["rgb.npy"] != (shape, np.dtype("uint8"))
        ):
            raise ValueError("Invalid seed NPZ shapes or dtypes")
    with np.load(path, allow_pickle=False, max_header_size=4096) as arrays:
        return arrays["xyz"], arrays["rgb"]


PLY_TYPES = {
    "char": "i1",
    "uchar": "u1",
    "short": "<i2",
    "ushort": "<u2",
    "int": "<i4",
    "uint": "<u4",
    "float": "<f4",
    "double": "<f8",
    "int8": "i1",
    "uint8": "u1",
    "int16": "<i2",
    "uint16": "<u2",
    "int32": "<i4",
    "uint32": "<u4",
    "float32": "<f4",
    "float64": "<f8",
}


def _seed_ply(path):
    with path.open("rb") as stream:
        if stream.readline(80).strip() != b"ply":
            raise ValueError("Invalid PLY magic")
        format_name, count, fields = None, None, []
        for _ in range(128):
            line = stream.readline(4096)
            if not line or len(line) >= 4096:
                raise ValueError("Invalid PLY header")
            parts = line.decode("ascii").strip().split()
            if not parts or parts[0] in ("comment", "obj_info"):
                continue
            if parts == ["end_header"]:
                break
            if (
                parts[0] == "format"
                and len(parts) == 3
                and parts[2] == "1.0"
                and format_name is None
            ):
                format_name = parts[1]
            elif (
                parts[:2] == ["element", "vertex"] and len(parts) == 3 and count is None
            ):
                count = int(parts[2])
            elif (
                parts[0] == "property"
                and len(parts) == 3
                and count is not None
                and parts[1] in PLY_TYPES
            ):
                fields.append((parts[2], PLY_TYPES[parts[1]]))
            else:
                raise ValueError("Only scalar vertex-only PLY clouds are supported")
        else:
            raise ValueError("PLY header exceeds limit")
        if (
            format_name not in ("binary_little_endian", "ascii")
            or count is None
            or not 100 <= count <= 1_000_000
        ):
            raise ValueError("Unsupported PLY format or point budget")
        names = [name for name, _ in fields]
        if (
            len(fields) > 32
            or len(set(names)) != len(names)
            or not {"x", "y", "z", "red", "green", "blue"} <= set(names)
        ):
            raise ValueError("PLY needs unique x/y/z/red/green/blue properties")
        dtype = np.dtype(fields)
        if any(dtype[name].kind != "f" for name in ("x", "y", "z")) or any(
            dtype[name] != np.uint8 for name in ("red", "green", "blue")
        ):
            raise ValueError("PLY positions must be floats and colors uint8")
        if format_name == "binary_little_endian":
            if stream.tell() + count * dtype.itemsize != path.stat().st_size:
                raise ValueError("PLY size differs from declared vertices")
            vertices = np.fromfile(stream, dtype=dtype, count=count)
        else:
            if path.stat().st_size > 256 << 20:
                raise ValueError("ASCII PLY size budget exceeded")
            values = np.loadtxt(stream, dtype=np.float64, max_rows=count, ndmin=2)
            if values.shape != (count, len(fields)) or stream.read().strip():
                raise ValueError("Invalid ASCII PLY vertex rows")
            vertices = np.empty(count, dtype=dtype)
            for column, (name, _) in enumerate(fields):
                data = values[:, column]
                if dtype[name].kind in "iu":
                    limits = np.iinfo(dtype[name])
                    if (
                        (data < limits.min)
                        | (data > limits.max)
                        | (data != np.floor(data))
                    ).any():
                        raise ValueError("Invalid PLY integer property")
                vertices[name] = data
        xyz = np.stack([vertices[name] for name in ("x", "y", "z")], axis=1)
        rgb = np.stack([vertices[name] for name in ("red", "green", "blue")], axis=1)
        return xyz, rgb


def load_seed(path, max_points, seed):
    if path.suffix.lower() == ".npz":
        xyz, rgb = _seed_npz(path)
    elif path.suffix.lower() == ".ply":
        xyz, rgb = _seed_ply(path)
    else:
        raise ValueError("Seed cloud must be xyz/rgb NPZ or scalar vertex PLY")
    if not np.isfinite(xyz).all() or (np.abs(xyz) > 1e6).any():
        raise ValueError("Seed coordinates must be finite and bounded")
    original_count = len(xyz)
    if original_count > max_points:
        keep = np.random.default_rng(seed).choice(
            original_count, max_points, replace=False
        )
        xyz, rgb = xyz[keep], rgb[keep]
    return xyz, rgb, original_count


def output_intrinsics(camera, width, height):
    target = camera["rectified"]["K"].copy()
    target[0] *= width / camera["rectified"]["width"]
    target[1] *= height / camera["rectified"]["height"]
    return target


def _opencv():
    try:
        import cv2
    except ImportError as error:
        raise RuntimeError(
            "Video decoding/resampling needs the separate rangle_preprocessing requirements"
        ) from error
    cv2.setNumThreads(1)
    return cv2


def _check_image_header(path, width, height):
    """Read bounded PNG/JPEG dimensions before asking OpenCV to allocate RGB."""
    if path.stat().st_size > 256 << 20:
        raise ValueError("Source image exceeds file budget")
    with path.open("rb") as stream:
        prefix = stream.read(26)
        if prefix[:8] == b"\x89PNG\r\n\x1a\n" and prefix[12:16] == b"IHDR":
            if len(prefix) != 26 or prefix[24:26] != bytes([8, 2]):
                raise ValueError("PNG sources must be 8-bit RGB without alpha")
            dimensions = struct.unpack(">II", prefix[16:24])
        elif prefix[:2] == b"\xff\xd8":
            stream.seek(2)
            dimensions = None
            while stream.tell() < 1 << 20:
                if stream.read(1) != b"\xff":
                    raise ValueError("Invalid JPEG marker")
                marker = stream.read(1)
                while marker == b"\xff":
                    marker = stream.read(1)
                if not marker or marker[0] in (0xD9, 0xDA):
                    break
                length_data = stream.read(2)
                if len(length_data) != 2:
                    break
                length = struct.unpack(">H", length_data)[0]
                if length < 2:
                    break
                if marker[0] in (
                    0xC0,
                    0xC1,
                    0xC2,
                    0xC3,
                    0xC5,
                    0xC6,
                    0xC7,
                    0xC9,
                    0xCA,
                    0xCB,
                    0xCD,
                    0xCE,
                    0xCF,
                ):
                    data = stream.read(5)
                    if len(data) == 5:
                        h, w = struct.unpack(">HH", data[1:5])
                        dimensions = (w, h)
                    break
                stream.seek(length - 2, 1)
        else:
            raise ValueError("Source images must be RGB NPY, PNG or JPEG")
    if dimensions != (width, height):
        raise ValueError("Image header dimensions differ from source calibration")


def decode_view(view, frames, output, files):
    camera, media = view["calibration"], view["media"]
    width, height = output["width"], output["height"]
    K = output_intrinsics(camera, width, height)
    identity = (
        width == camera["width"]
        and height == camera["height"]
        and not np.any(camera["distortion"])
        and np.array_equal(K, camera["K"])
    )
    mapping, cv2, capture = None, None, None
    if (
        not identity
        or "video" in media
        or any(not name.lower().endswith(".npy") for name in media.get("images", []))
    ):
        cv2 = _opencv()
        mapping = cv2.initUndistortRectifyMap(
            camera["K"],
            camera["distortion"] if camera["distortion"].size else None,
            None,
            K,
            (width, height),
            cv2.CV_32FC1,
        )
    selected = []
    source_indices = frames["indices"]
    paths = []
    if "video" in media:
        path = files.get(media["video"], f"view:{view['id']}:video")
        capture = cv2.VideoCapture(str(path))
        if not capture.isOpened():
            capture.release()
            raise ValueError(f"Cannot open video for view {view['id']}")
        actual_fps = capture.get(cv2.CAP_PROP_FPS)
        if not math.isfinite(actual_fps) or abs(actual_fps - frames["fps"]) > 1e-6:
            capture.release()
            raise ValueError(
                "Video fps differs from the explicit synchronized source fps"
            )
        if (
            int(capture.get(cv2.CAP_PROP_FRAME_WIDTH)),
            int(capture.get(cv2.CAP_PROP_FRAME_HEIGHT)),
        ) != (camera["width"], camera["height"]):
            capture.release()
            raise ValueError("Video dimensions differ from source calibration")
    else:
        paths = [
            files.get(name, f"view:{view['id']}:image") for name in media["images"]
        ]
    try:
        decoded_index = -1
        for position, source_index in enumerate(source_indices):
            if capture is not None:
                while decoded_index < source_index:
                    ok, pixels = capture.read()
                    if not ok:
                        raise ValueError("Missing selected video frame")
                    decoded_index += 1
                rgb = cv2.cvtColor(pixels, cv2.COLOR_BGR2RGB)
            else:
                path = paths[position]
                if path.suffix.lower() == ".npy":
                    with path.open("rb") as stream:
                        shape, dtype = _npy_header(
                            stream, path.stat().st_size, 192_000_000
                        )
                    if (
                        shape != (camera["height"], camera["width"], 3)
                        or dtype != np.uint8
                    ):
                        raise ValueError("Image NPY must be calibrated uint8 RGB")
                    rgb = np.load(path, allow_pickle=False, max_header_size=4096)
                else:
                    _check_image_header(path, camera["width"], camera["height"])
                    pixels = cv2.imread(str(path), cv2.IMREAD_COLOR)
                    if pixels is None:
                        raise ValueError("Cannot decode source image")
                    rgb = cv2.cvtColor(pixels, cv2.COLOR_BGR2RGB)
            if (
                rgb.shape != (camera["height"], camera["width"], 3)
                or rgb.dtype != np.uint8
            ):
                raise ValueError(
                    "Decoded RGB dimensions differ from source calibration"
                )
            if not identity:
                rgb = cv2.remap(rgb, *mapping, interpolation=cv2.INTER_LINEAR)
            selected.append(rgb)
    finally:
        if capture is not None:
            capture.release()
    return np.stack(selected), K
