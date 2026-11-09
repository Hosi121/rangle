"""Read recorded synchronized views only as their original frame PTS arrives."""

import time
from collections import deque
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

import numpy as np
from rangle_preprocessing.schema import (
    local_path,
    normalize_geometry,
    read_manifest,
    validate_views,
)
from rangle_preprocessing.sources import SourceFiles, load_seed, output_intrinsics
from rangle_preprocessing.vru import adapt_vru
from rangle_training.data import Dataset, scene_camera

from .window import Frame, Window


class Rig:
    def __init__(self, manifest, root):
        self.config, self.manifest_sha = read_manifest(manifest, include_checksum=True)
        self.root = Path(root).resolve(strict=True)
        files = SourceFiles(self.root, self.config.get("checksums", {}))
        views = (
            adapt_vru(self.config, files)
            if self.config["adapter"] == "vru-colmap"
            else self.config["views"]
        )
        self.views = validate_views(views, self.config)
        self.width, self.height = (
            self.config["output"][key] for key in ("width", "height")
        )
        self.window_frames = len(self.config["frames"]["indices"])
        if not 2 <= self.window_frames <= 32:
            raise ValueError("Temporal windows require 2 to 32 frames")
        if any(set(view["media"]) != {"video"} for view in self.views):
            raise ValueError("Live input must decode videos, not prepared image arrays")
        self.train = np.array(
            [i for i, view in enumerate(self.views) if view["split"] == "train"],
            np.int64,
        )
        self.heldout = np.array(
            [i for i, view in enumerate(self.views) if view["split"] == "heldout"],
            np.int64,
        )
        self.fps = float(self.config["frames"]["fps"])
        self.video_paths = [
            local_path(self.root, view["media"]["video"]) for view in self.views
        ]
        self.seed_path = files.get(self.config["seed"]["path"], "initial-seed")
        if set(self.config.get("checksums", {})) - files.checked:
            raise ValueError(
                "Live startup checksums must reference verified calibration/seed files"
            )
        matrices = np.stack(
            [view["calibration"]["world_to_camera"] for view in self.views]
        )
        _, self.matrices, self.normalization = normalize_geometry(
            np.zeros((0, 3)), matrices, self.train, self.config["normalization"]
        )
        self.target_Ks = np.stack(
            [
                output_intrinsics(view["calibration"], self.width, self.height)
                for view in self.views
            ]
        )
        self.Ks = self.target_Ks.astype(np.float32)
        self.provenance = {
            "manifest_sha256": self.manifest_sha,
            "geometry_files": list(files.records.values()),
            "source": self.config["source"],
            "normalization": self.normalization,
            "trained_model_warm_start": False,
            "caveat": "Precomputed author calibration/seed geometry may contain source-camera and temporal information; arrived-image optimization is causal, geometry is not claimed fully causal.",
        }
        self.xyz = self.rgb = self.camera = None

    def admit_seed(self, arrived_time, usable_after):
        if arrived_time + 1e-9 < usable_after:
            raise ValueError(
                "Seed prior cannot be admitted before its declared source time"
            )
        xyz, self.rgb, count = load_seed(
            self.seed_path,
            self.config["seed"]["max_points"],
            self.config["seed"]["seed"],
        )
        centre = np.asarray(self.normalization["centre"])
        self.xyz = ((xyz - centre) / self.normalization["scale"]).astype(np.float32)
        dummy = Dataset(
            np.empty((0, len(self.views), self.height, self.width, 3), np.uint8),
            self.matrices,
            self.Ks,
            self.xyz,
            self.rgb,
            self.train,
            self.heldout,
            np.array([0, 1 / self.fps], np.float64),
        )
        self.camera = scene_camera(dummy)
        self.provenance.update(
            seed_points=count,
            seed_usable_after_seconds=usable_after,
            seed_admitted_at_input_time_seconds=arrived_time,
        )


class VideoDecoder:
    def __init__(self, rig, workers=8):
        import cv2

        self.cv2, self.rig, self.captures, self.maps = cv2, rig, [], []
        cv2.setNumThreads(1)
        if not 1 <= workers <= 32:
            raise ValueError("Decode workers must be bounded to 1 through 32")
        self.workers = workers
        self.profile = {
            "decoded_frame_sets": 0,
            "decode_workers": workers,
            "sum_view_decode_wall_seconds": 0.0,
            "sum_view_remap_wall_seconds": 0.0,
            "frame_set_read_wall_seconds": 0.0,
        }
        self.pool = ThreadPoolExecutor(
            max_workers=workers, thread_name_prefix="live-decode"
        )
        try:
            for path, view in zip(rig.video_paths, rig.views, strict=True):
                cap = cv2.VideoCapture(
                    str(path), cv2.CAP_FFMPEG, [cv2.CAP_PROP_N_THREADS, 1]
                )
                self.captures.append(cap)
                camera = view["calibration"]
                if (
                    not cap.isOpened()
                    or abs(cap.get(cv2.CAP_PROP_FPS) - rig.fps) > 1e-6
                ):
                    raise ValueError(
                        "Video cannot be opened at the declared original frame rate"
                    )
                if (
                    int(cap.get(cv2.CAP_PROP_FRAME_WIDTH)),
                    int(cap.get(cv2.CAP_PROP_FRAME_HEIGHT)),
                ) != (camera["width"], camera["height"]):
                    raise ValueError("Source video and calibration dimensions differ")
                self.maps.append(
                    cv2.initUndistortRectifyMap(
                        camera["K"],
                        camera["distortion"],
                        None,
                        rig.target_Ks[len(self.maps)],
                        (rig.width, rig.height),
                        cv2.CV_32FC1,
                    )
                )
        except BaseException:
            self.close()
            raise

    def _read(self, index):
        cap = self.captures[index]
        began = time.monotonic()
        ok, image = cap.read()
        if not ok:
            raise ValueError("Source ended before the configured finite duration")
        pts = cap.get(self.cv2.CAP_PROP_POS_MSEC) / 1000
        decoded = time.monotonic()
        image = self.cv2.remap(
            image, *self.maps[index], interpolation=self.cv2.INTER_LINEAR
        )
        image = self.cv2.cvtColor(image, self.cv2.COLOR_BGR2RGB)
        return pts, image, decoded - began, time.monotonic() - decoded

    def read(self, number):
        began = time.monotonic()
        views = list(self.pool.map(self._read, range(len(self.captures))))
        pts = np.array([item[0] for item in views])
        if (
            not np.isfinite(pts).all()
            or np.ptp(pts) > 1e-5
            or abs(pts[0] - number / self.rig.fps) > 1e-5
        ):
            raise ValueError(
                "Original PTS must be synchronized and match the source cadence"
            )
        images = np.stack([item[1] for item in views])
        images.flags.writeable = False
        old = self.profile
        self.profile = {
            **old,
            "decoded_frame_sets": old["decoded_frame_sets"] + 1,
            "sum_view_decode_wall_seconds": old["sum_view_decode_wall_seconds"]
            + sum(row[2] for row in views),
            "sum_view_remap_wall_seconds": old["sum_view_remap_wall_seconds"]
            + sum(row[3] for row in views),
            "frame_set_read_wall_seconds": old["frame_set_read_wall_seconds"]
            + time.monotonic()
            - began,
        }
        return float(pts[0]), images

    def close(self):
        self.pool.shutdown(wait=True, cancel_futures=True)
        for cap in self.captures:
            cap.release()


def produce(
    decoder,
    bus,
    stop,
    origin,
    frames,
    fps,
    window_frames,
    seed_after,
    observe,
    *,
    clock=time.monotonic,
    wait=None,
):
    """No decoded frame is requested before its source PTS. One pending window."""
    wait = stop.wait if wait is None else wait
    ring = deque(maxlen=window_frames)
    offered, last_offered = 0, -1
    try:
        for number in range(frames):
            delay = origin + number / fps - clock()
            if delay > 0 and wait(delay):
                break
            if stop.is_set():
                break
            pts, images = decoder.read(number)
            now = clock()
            if pts > now - origin + 1e-6:
                raise ValueError("Decoder exposed a future capture time")
            frame = Frame(number, pts, origin + pts, now, images)
            ring.append(frame)
            bus.observe(frame)
            if (
                len(ring) == window_frames
                and pts + 1e-9 >= seed_after
                and (offered == 0 or number - last_offered >= window_frames)
            ):
                offered += 1
                bus.offer(Window(tuple(ring), offered))
                last_offered = number
            observe(bus.status())
        if (
            not stop.is_set()
            and len(ring) == window_frames
            and ring[-1].number != last_offered
            and ring[-1].pts + 1e-9 >= seed_after
        ):
            offered += 1
            bus.offer(Window(tuple(ring), offered))
        bus.finish(clock())
        observe(bus.status(), force=True)
    except Exception as error:  # noqa: BLE001 - transfer worker failure to consumer
        bus.finish(clock(), error)
    finally:
        decoder.close()
