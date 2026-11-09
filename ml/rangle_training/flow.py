"""One offline observed-flow ablation; no CLI, network, weights or wire changes.

DIS_MEDIUM (OpenCV 4.13, one CPU thread) observes arrived adjacent RGB frames
from camera IDs 1/11 only. Pixel coordinates are OpenCV x-right/y-down with
positive camera Z. World-to-camera matrices and K are the renderer's matrices.

The fixed auxiliary is 0.01 * mean_xy(Huber(pixel error, delta=1px)) / width,
averaged with detached FB-confidence/lifetime weights. Detached projection and
sampling geometry prevent moving means/lifetimes to evade observations; only
velocity receives this gradient. Canonical row sampling uses at most 4096
Gaussians and no RNG. This has no occlusion/transmittance visibility model and
is a small rangle hypothesis, not MoRGS reproduction or measured improvement.
"""

import math
import time
from dataclasses import dataclass

import numpy as np

from .temporal import temporal_values

CAMERA_IDS = ("1", "11")
MAX_SAMPLES = 4096
MAX_FLOW_BYTES = 128 << 20


def _inputs(images, times, numbers, view_ids, train, heldout, arrived):
    if not isinstance(images, (np.ndarray, list, tuple)) or not 2 <= len(images) <= 32:
        raise ValueError("Observed flow requires 2 to 32 arrived frames")
    if any(not isinstance(frame, np.ndarray) for frame in images):
        raise ValueError("Observed RGB frames must be arrays")
    shape = images[0].shape
    if (
        len(shape) != 4
        or not 4 <= shape[0] <= 64
        or min(shape[1:3]) < 16
        or shape[3] != 3
        or any(frame.shape != shape or frame.dtype != np.uint8 for frame in images)
        or sum(frame.nbytes for frame in images) > 512 << 20
    ):
        raise ValueError("Invalid or oversized observed RGB frames")
    times, numbers = np.asarray(times), np.asarray(numbers)
    if (
        times.shape != (len(images),)
        or numbers.shape != times.shape
        or times.dtype.kind != "f"
        or numbers.dtype.kind not in "iu"
        or not np.isfinite(times).all()
        or times[0] < 0
        or not np.allclose(np.diff(times), 1 / 25, rtol=0, atol=1e-9)
        or (numbers < 0).any()
        or not (np.diff(numbers.astype(np.int64)) == 1).all()
        or not np.allclose(times, numbers / 25, rtol=0, atol=1e-9)
        or not math.isfinite(arrived)
        or times[-1] > arrived + 1e-9
    ):
        raise ValueError("Flow requires consecutive arrived 25fps source times")
    ids = tuple(str(value) for value in view_ids)
    if len(ids) != shape[0] or len(set(ids)) != len(ids):
        raise ValueError("Camera IDs must match the image view axis")
    splits = []
    for values in (train, heldout):
        values = np.asarray(values)
        if (
            values.ndim != 1
            or values.dtype.kind not in "iu"
            or not len(values)
            or len(set(values.tolist())) != len(values)
            or (values < 0).any()
            or (values >= shape[0]).any()
        ):
            raise ValueError("Invalid observed-flow view split")
        splits.append(set(values.tolist()))
    if splits[0] & splits[1] or splits[0] | splits[1] != set(range(shape[0])):
        raise ValueError("Training and heldout views must partition the rig")
    if not set(CAMERA_IDS) <= set(ids):
        raise ValueError("Observed camera IDs 1/11 are required")
    selected = tuple(ids.index(value) for value in CAMERA_IDS)
    if not set(selected) <= splits[0]:
        raise ValueError("Observed cameras must be training views only")
    height, width = shape[1:3]
    if (len(images) - 1) * len(selected) * height * width * 12 > MAX_FLOW_BYTES:
        raise ValueError("Observed flow exceeds its fixed memory budget")
    return times.astype(np.float64), numbers.astype(np.int64), selected


def forward_backward_confidence(forward, backward):
    """Detach a <=1px consistency mask, including destination image bounds."""
    import cv2

    if (
        forward.shape != backward.shape
        or forward.ndim != 3
        or forward.shape[2] != 2
        or not np.isfinite(forward).all()
        or not np.isfinite(backward).all()
    ):
        raise ValueError("Invalid forward/backward flow")
    height, width = forward.shape[:2]
    y, x = np.indices((height, width), dtype=np.float32)
    target_x, target_y = x + forward[..., 0], y + forward[..., 1]
    reverse = cv2.remap(
        backward, target_x, target_y, cv2.INTER_LINEAR, borderMode=cv2.BORDER_CONSTANT
    )
    return (
        (target_x >= 0)
        & (target_x <= width - 1)
        & (target_y >= 0)
        & (target_y <= height - 1)
        & (np.linalg.norm(forward + reverse, axis=-1) <= 1)
    )


def _projected_loss(params, first, second, viewmat, K, observation):
    import torch
    import torch.nn.functional as F

    viewmat, K = viewmat.detach(), K.detach()
    _, _, height, width = observation.shape
    count = len(params["means"])
    if not count:
        zero = params["velocities"].sum() * 0
        return zero, zero.detach(), 0
    step = max(1, math.ceil(count / MAX_SAMPLES))
    indices = torch.arange(0, count, step, device=params["means"].device)
    velocity = params["velocities"][indices]
    with torch.no_grad():
        selected = {
            name: params[name][indices]
            for name in (
                "means",
                "velocities",
                "opacities",
                "centre_times",
                "log_sigmas",
            )
        }
        means, opacity0 = temporal_values(selected, first)
        _, opacity1 = temporal_values(selected, second)
        camera0 = means @ viewmat[:3, :3].T + viewmat[:3, 3]
        projected0 = camera0 @ K.T
        pixels0 = projected0[:, :2] / projected0[:, 2:3].clamp_min(1e-6)
        source_valid = (
            (camera0[:, 2] > 1e-6)
            & (pixels0[:, 0] >= 0)
            & (pixels0[:, 0] <= width - 1)
            & (pixels0[:, 1] >= 0)
            & (pixels0[:, 1] <= height - 1)
        )
        grid = torch.where(source_valid[:, None], pixels0, 0)
        grid = grid / grid.new_tensor([width - 1, height - 1]) * 2 - 1
        sampled = F.grid_sample(
            observation, grid[None, :, None], mode="bilinear", align_corners=True
        )[0, :, :, 0].T
        # A mask interpolated across an invalid neighbour is not confidence 1.
        confidence = sampled[:, 2] >= 1 - 1e-6
        support = torch.minimum(opacity0, opacity1)
    camera1 = camera0 + (velocity @ viewmat[:3, :3].T) * (second - first)
    projected1 = camera1 @ K.T
    pixels1 = projected1[:, :2] / projected1[:, 2:3].clamp_min(1e-6)
    with torch.no_grad():
        valid = (
            source_valid
            & confidence
            & (camera1[:, 2] > 1e-6)
            & (pixels1[:, 0] >= 0)
            & (pixels1[:, 0] <= width - 1)
            & (pixels1[:, 1] >= 0)
            & (pixels1[:, 1] <= height - 1)
        )
        weights = support * valid
    error = torch.where(valid[:, None], pixels1 - pixels0 - sampled[:, :2], 0)
    huber = F.huber_loss(
        error, torch.zeros_like(error), delta=1, reduction="none"
    ).mean(dim=1)
    loss = 0.01 / width * (huber * weights).sum() / weights.sum().clamp_min(1e-8)
    return loss, (weights > 0).float().mean().detach(), len(indices)


@dataclass
class ObservedFlow:
    fields: dict
    frame_times: np.ndarray
    frame_numbers: np.ndarray
    view_indices: tuple
    metadata: dict
    applied_steps: int = 0
    loss_submission_seconds: float = 0
    valid_sum: object = None

    def validate_window(self, window):
        if tuple(frame.number for frame in window.frames) != tuple(
            self.frame_numbers
        ) or not np.array_equal(
            [frame.pts for frame in window.frames], self.frame_times
        ):
            raise ValueError("Observed flow does not match this arrived window")

    def loss(self, params, frame, view, viewmat, K):
        field = self.fields.get((frame, view))
        if field is None:
            return params["velocities"][:1].sum() * 0
        began = time.perf_counter()
        start = self.frame_times[0]
        loss, valid, samples = _projected_loss(
            params,
            float(self.frame_times[frame - 1] - start),
            float(self.frame_times[frame] - start),
            viewmat,
            K,
            field,
        )
        self.loss_submission_seconds += time.perf_counter() - began
        self.applied_steps += 1
        self.valid_sum = valid if self.valid_sum is None else self.valid_sum + valid
        self.metadata["flow_last_gaussian_samples"] = samples
        return loss

    def metrics(self):
        return {
            **self.metadata,
            "flow_auxiliary_steps": self.applied_steps,
            "flow_projected_valid_fraction": float(self.valid_sum) / self.applied_steps
            if self.applied_steps
            else 0,
            # CUDA work is asynchronous. Total training_seconds includes GPU
            # execution; this number measures CPU dispatch, not GPU kernel time.
            "flow_loss_cpu_submission_seconds": self.loss_submission_seconds,
        }


def prepare_observed_flow(
    images,
    frame_times_seconds,
    frame_numbers,
    *,
    view_ids,
    train_indices,
    heldout_indices,
    latest_arrived_time_seconds,
    device="cpu",
):
    """Prepare only camera 1/11 from arrived frames; do not read heldout pixels."""
    times, numbers, selected = _inputs(
        images,
        frame_times_seconds,
        frame_numbers,
        view_ids,
        train_indices,
        heldout_indices,
        latest_arrived_time_seconds,
    )
    import cv2
    import torch

    if not cv2.__version__.startswith("4.13."):
        raise RuntimeError("The fixed offline candidate requires OpenCV 4.13")
    began, prior_threads = time.perf_counter(), cv2.getNumThreads()
    fields, fractions = {}, []
    tensor_seconds = 0
    cv2.setNumThreads(1)
    try:
        for view in selected:
            gray0 = cv2.cvtColor(images[0][view], cv2.COLOR_RGB2GRAY)
            engine = cv2.DISOpticalFlow_create(cv2.DISOPTICAL_FLOW_PRESET_MEDIUM)
            for frame in range(1, len(images)):
                gray1 = cv2.cvtColor(images[frame][view], cv2.COLOR_RGB2GRAY)
                forward = engine.calc(gray0, gray1, None)
                backward = engine.calc(gray1, gray0, None)
                mask = forward_backward_confidence(forward, backward)
                fractions.append(float(mask.mean()))
                observation = np.concatenate([forward, mask[..., None]], axis=2)
                observation = np.ascontiguousarray(
                    observation.transpose(2, 0, 1)[None], dtype=np.float32
                )
                tensor_started = time.perf_counter()
                fields[frame, view] = torch.as_tensor(observation, device=device)
                tensor_seconds += time.perf_counter() - tensor_started
                gray0 = gray1
    finally:
        cv2.setNumThreads(prior_threads)
    metadata = {
        "flow_profile": "offline-observed-dis-medium-v1",
        "flow_opencv_version": cv2.__version__,
        "flow_cpu_threads": 1,
        "flow_camera_ids": list(CAMERA_IDS),
        "flow_view_indices": list(selected),
        "flow_pairs": len(fields),
        "flow_max_gaussian_samples": MAX_SAMPLES,
        "flow_weight": 0.01,
        "flow_huber_delta_pixels": 1,
        "flow_consistency_pixels": 1,
        "flow_fb_valid_fraction": float(np.mean(fractions)),
        "flow_prepare_seconds": time.perf_counter() - began,
        "flow_tensor_submission_seconds": tensor_seconds,
        "flow_tensor_bytes": sum(
            value.numel() * value.element_size() for value in fields.values()
        ),
        "flow_latest_observed_time_seconds": float(times[-1]),
        "flow_source_frames": numbers.tolist(),
    }
    return ObservedFlow(fields, times, numbers, selected, metadata)
