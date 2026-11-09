"""Immutable model/metrics/scene, followed by an atomic bounded live index."""

import hashlib
import json
import os
import re
import shutil
import tempfile
import threading
import time
from datetime import timedelta
from pathlib import Path

from rangle_training.packing import temporal_header


def json_bytes(value):
    return (
        json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        + "\n"
    ).encode()


def immutable_file(path, data):
    with Path(path).open("xb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def atomic_index(path, data):
    if len(data) > 512 << 10:
        raise ValueError("Live index exceeds 512 KiB")
    descriptor, temporary = tempfile.mkstemp(prefix=".index-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


class Publisher:
    def __init__(
        self,
        root,
        session_id,
        title,
        source,
        origin_monotonic,
        origin_utc,
        clock=time.monotonic,
    ):
        if (
            not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", session_id)
            or not title
            or len(title) > 256
        ):
            raise ValueError("Invalid live session identity")
        self.root = Path(root)
        self.root.mkdir(parents=True, exist_ok=False)
        self.session_dir = self.root / "live-assets" / session_id
        self.session_dir.mkdir(parents=True)
        self.origin, self.origin_utc, self.clock = origin_monotonic, origin_utc, clock
        self.lock = threading.Lock()
        self.last_write = float("-inf")
        self.first_available = None
        self.camera_fingerprint = None
        self.session = {
            "id": session_id,
            "title": title,
            "state": "starting",
            "started_at": self.timestamp(origin_monotonic),
            "updated_at": self.timestamp(origin_monotonic),
            "target_latency_seconds": 30,
            "latest_input_time_seconds": 0.0,
            "dropped_windows": 0,
            "source": source,
            "segments": [],
        }
        self._write()

    def timestamp(self, monotonic):
        return (
            (self.origin_utc + timedelta(seconds=monotonic - self.origin))
            .isoformat(timespec="milliseconds")
            .replace("+00:00", "Z")
        )

    def _write(self):
        now = self.clock()
        self.session["updated_at"] = self.timestamp(now)
        atomic_index(
            self.root / "index.json",
            json_bytes({"version": 1, "sessions": [self.session]}),
        )
        self.last_write = now

    def observe(self, status, force=False):
        with self.lock:
            self.session["latest_input_time_seconds"] = status[
                "latest_input_time_seconds"
            ]
            self.session["dropped_windows"] = status["dropped_windows"]
            if force or self.clock() - self.last_write >= 1:
                self._write()

    def publish(self, payload, metrics, window, camera, status):
        count, frames, fps, duration, _ = temporal_header(payload)
        if (
            frames != len(window.frames)
            or abs(duration - (window.end - window.start)) > 1e-5
        ):
            raise ValueError("Model timeline differs from the arrived source window")
        if window.end > status["latest_input_time_seconds"] + 1e-9:
            raise ValueError("Snapshot includes unarrived pixels")
        fingerprint = json.dumps(camera, sort_keys=True, allow_nan=False)
        with self.lock:
            if (
                self.camera_fingerprint is not None
                and self.camera_fingerprint != fingerprint
            ):
                raise ValueError("Session camera/world convention must remain fixed")
            sequence = getattr(self, "last_sequence", 0) + 1
            if sequence > 999999:
                raise ValueError("Live sequence exhausted")
            digest = hashlib.sha256(payload).hexdigest()
            revision = f"{sequence:06d}-{digest[:16]}"
            destination = self.session_dir / revision
            if destination.exists():
                raise FileExistsError("Live revisions are immutable")
            temporary = Path(tempfile.mkdtemp(prefix=".pending-", dir=self.session_dir))
            try:
                immutable_file(temporary / "model.rgs", payload)
                local_metrics = {
                    **metrics,
                    "model_sha256": digest,
                    "model_bytes": len(payload),
                    "stored_gaussians": count,
                    "frame_rate": fps,
                    "frame_count": frames,
                    "source": self.session["source"],
                    "captured_at": self.timestamp(window.frames[-1].captured_monotonic),
                }
                immutable_file(temporary / "metrics.json", json_bytes(local_metrics))
                scene = {
                    "version": 1,
                    "frameRate": fps,
                    "frameCount": frames,
                    "temporal": {"url": "model.rgs"},
                    "camera": camera,
                    "attribution": {
                        "label": self.session["title"],
                        "source": self.session["source"]["url"],
                        "license": self.session["source"]["license"],
                    },
                }
                immutable_file(temporary / "scene.json", json_bytes(scene))
                os.rename(temporary, destination)
            finally:
                if temporary.exists():
                    shutil.rmtree(temporary)
            available = self.clock()
            segment = {
                "sequence": sequence,
                "start_seconds": window.start,
                "end_seconds": window.end,
                "scene_url": f"/live-assets/{self.session['id']}/{revision}/scene.json",
                "model_sha256": digest,
                "model_bytes": len(payload),
                "captured_at": self.timestamp(window.frames[-1].captured_monotonic),
                "available_at": self.timestamp(available),
                "training_seconds": metrics["training_seconds"],
                "mean_packed_psnr": metrics["mean_packed_psnr"],
                "mean_packed_ssim": metrics["mean_packed_ssim"],
            }
            self.session["segments"] = (self.session["segments"] + [segment])[-32:]
            self.session["state"] = "live"
            self.session["latest_input_time_seconds"] = max(
                self.session["latest_input_time_seconds"],
                status["latest_input_time_seconds"],
            )
            self.session["dropped_windows"] = max(
                self.session["dropped_windows"], status["dropped_windows"]
            )
            self._write()
            self.last_sequence = sequence
            self.camera_fingerprint = fingerprint
            if self.first_available is None:
                self.first_available = available
            return {
                **segment,
                "capture_to_local_snapshot_seconds": available
                - window.frames[-1].captured_monotonic,
                "arrival_to_local_snapshot_seconds": available
                - window.frames[-1].arrived_monotonic,
                "available_monotonic": available,
            }

    def finish(self, state, status):
        if state not in ("ended", "failed"):
            raise ValueError("Invalid terminal live state")
        with self.lock:
            self.session["state"] = state
            self.session["latest_input_time_seconds"] = status[
                "latest_input_time_seconds"
            ]
            self.session["dropped_windows"] = status["dropped_windows"]
            self._write()
