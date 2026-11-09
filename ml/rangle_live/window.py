"""One bounded pending window; old work is replaced, never queued indefinitely."""

import threading
import time
from dataclasses import dataclass

import numpy as np


@dataclass(frozen=True)
class Frame:
    number: int
    pts: float
    captured_monotonic: float
    arrived_monotonic: float
    images: np.ndarray


@dataclass(frozen=True)
class Window:
    frames: tuple[Frame, ...]
    offered_number: int

    @property
    def start(self):
        return self.frames[0].pts

    @property
    def end(self):
        return self.frames[-1].pts


class LatestWindow:
    def __init__(self):
        self._condition = threading.Condition()
        self._pending = None
        self._done = False
        self._error = None
        self.dropped = 0
        self.latest_input_time = 0.0
        self.frames_received = 0
        self.finished_monotonic = None

    def observe(self, frame):
        with self._condition:
            if frame.pts < self.latest_input_time:
                raise ValueError("Input PTS moved backwards")
            self.latest_input_time = frame.pts
            self.frames_received += 1

    def offer(self, window):
        with self._condition:
            if self._done:
                raise RuntimeError("Cannot offer a window after source completion")
            if window.end > self.latest_input_time + 1e-9:
                raise ValueError("Window contains an unarrived input time")
            if self._pending is not None:
                self.dropped += 1
            self._pending = window
            self._condition.notify()

    def take(self, deadline=None):
        with self._condition:
            ready = self._condition.wait_for(
                lambda: self._pending is not None or self._done,
                timeout=None
                if deadline is None
                else max(0, deadline - time.monotonic()),
            )
            if not ready:
                raise TimeoutError("Finite live-worker input deadline exceeded")
            if self._error is not None:
                raise RuntimeError("Input producer failed") from self._error
            window = self._pending
            self._pending = None
            return window

    def finish(self, now, error=None):
        with self._condition:
            self._done = True
            self.finished_monotonic = now
            self._error = error
            self._condition.notify_all()

    def status(self):
        with self._condition:
            return {
                "latest_input_time_seconds": self.latest_input_time,
                "dropped_windows": self.dropped,
                "frames_received": self.frames_received,
                "source_finished": self._done,
                "source_finished_monotonic": self.finished_monotonic,
                "pending_windows": int(self._pending is not None),
            }
