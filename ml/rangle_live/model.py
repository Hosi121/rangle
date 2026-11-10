"""Persistent Gaussian/Adam state updated only from one arrived image window."""

import hashlib
import importlib.util
import math
import sys
import time
from pathlib import Path

import numpy as np
from rangle_training.packing import compress_temporal_rle as compress_temporal
from rangle_training.packing import pack_temporal, unpack_temporal
from rangle_training.temporal import (
    _ssim_function,
    initialize_parameters,
    stored_values,
    temporal_values,
)


def bootstrap_cuda(extension_path=None):
    import gsplat
    import torch

    result = {
        "torch": torch.__version__,
        "cuda": torch.version.cuda,
        "gsplat": gsplat.__version__,
    }
    if extension_path is not None:
        path = Path(extension_path).resolve(strict=True)
        spec = importlib.util.spec_from_file_location("gsplat_cuda", path)
        extension = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(extension)
        sys.modules["gsplat.csrc"] = extension
        gsplat.csrc = extension
        with path.open("rb") as stream:
            result["compiled_kernel_sha256"] = hashlib.file_digest(
                stream, "sha256"
            ).hexdigest()
    if not torch.cuda.is_available():
        raise RuntimeError("The live worker requires a CUDA GPU")
    result["gpu"] = torch.cuda.get_device_name()
    torch.set_num_threads(4)
    torch.manual_seed(0)
    torch.cuda.init()
    return result


def warmup_cuda():
    """Load compiled forward/backward kernels before the capture epoch, without RNG."""
    import torch
    from gsplat import rasterization

    def tensor(value, gradient=False):
        return torch.tensor(
            value, dtype=torch.float32, device="cuda", requires_grad=gradient
        )

    image = rasterization(
        means=tensor([[0, 0, 2]], True),
        scales=tensor([[0.1, 0.1, 0.1]], True),
        quats=tensor([[1, 0, 0, 0]], True),
        colors=tensor([[0.5, 0.5, 0.5]], True),
        opacities=tensor([0.1], True),
        viewmats=torch.eye(4, device="cuda")[None],
        Ks=tensor([[[16, 0, 8], [0, 16, 8], [0, 0, 1]]]),
        width=16,
        height=16,
        packed=False,
    )[0]
    if not image.requires_grad:
        raise RuntimeError("Warmup render produced no differentiable pixels")
    (
        image.mean() + 1 - _ssim_function("cuda")(image, torch.zeros_like(image))
    ).backward()
    torch.cuda.synchronize()


def advance_local_window(params, delta):
    """Predict the next window's geometry while resetting its lifetime phase.

    x_new(t)=x_old(t+delta); opacity_new(t)=opacity_old(t). This deliberately
    re-centres temporal support, rather than preserving a global lifetime gate.
    The parameter objects and Adam moments remain intact.
    """
    if not math.isfinite(delta) or delta < 0:
        raise ValueError("Window start must move forwards")
    import torch

    with torch.no_grad():
        params["means"].add_(params["velocities"], alpha=delta)


class IncrementalModel:
    def __init__(self, rig, window_frames, device="cuda"):
        import torch
        from gsplat import DefaultStrategy, rasterization
        from scipy.spatial import cKDTree

        self.torch, self.rasterization, self.device = torch, rasterization, device
        self.rig, self.window_start, self.total_steps = rig, None, 0
        self.rng = np.random.default_rng(0)
        selected = self.rng.choice(
            len(rig.xyz), min(100_000, len(rig.xyz)), replace=False
        )
        xyz = rig.xyz[selected].astype(np.float32)
        distances = cKDTree(xyz).query(xyz, k=4, workers=1)[0][:, 1:]
        spacing = np.sqrt(np.mean(distances**2, axis=1)).clip(1e-4, 1)
        self.duration = (window_frames - 1) / rig.fps
        times = np.arange(window_frames, dtype=np.float64) / rig.fps
        self.params, self.optimizers, self.rates = initialize_parameters(
            xyz,
            rig.rgb[selected],
            spacing,
            times,
            device,
            quality_profile="local-lifetime",
        )
        self.initial_count = len(xyz)
        self.strategy = DefaultStrategy(refine_stop_iter=3000)
        self.strategy.check_sanity(self.params, self.optimizers)
        self.strategy_state = self.strategy.initialize_state(scene_scale=1.0)
        self.views, self.Ks = self.tensor(rig.matrices), self.tensor(rig.Ks)
        self.ssim = _ssim_function(device)
        self.images = self.times = None

    def tensor(self, value):
        return self.torch.as_tensor(value, dtype=self.torch.float32, device=self.device)

    def _sync(self):
        if self.device == "cuda":
            self.torch.cuda.synchronize()

    def render(self, frame, view, arrays=None):
        p = self.params
        if arrays is None:
            means, opacity = temporal_values(p, self.times[frame])
            values = (
                means,
                p["scales"].exp(),
                p["quats"],
                p["colors"].sigmoid(),
                opacity,
            )
        else:
            values = stored_values(arrays, self.times[frame])
        means, scales, quats, colors, opacity = values
        return self.rasterization(
            means=means,
            scales=scales,
            quats=quats,
            colors=colors,
            opacities=opacity,
            viewmats=self.views[view : view + 1],
            Ks=self.Ks[view : view + 1],
            width=self.rig.width,
            height=self.rig.height,
            packed=False,
        )

    def fit(self, window, steps, deadline):
        torch, p = self.torch, self.params
        if self.window_start is not None:
            advance_local_window(p, window.start - self.window_start)
        self.window_start = window.start
        self.images = torch.as_tensor(
            np.stack([f.images for f in window.frames]),
            dtype=torch.uint8,
            device=self.device,
        )
        local_times = np.array([frame.pts - window.start for frame in window.frames])
        if abs(local_times[-1] - self.duration) > 1e-5:
            raise ValueError("Window duration changed")
        self.times = self.tensor(local_times)
        sampled = np.zeros(len(window.frames), np.int64)
        self._sync()
        began = time.monotonic()
        for _ in range(steps):
            if time.monotonic() >= deadline:
                raise TimeoutError("Finite live-worker training deadline exceeded")
            frame = int(self.rng.integers(len(window.frames)))
            view = int(self.rng.choice(self.rig.train))
            sampled[frame] += 1
            target = self.images[frame, view : view + 1].float() / 255
            image, _, info = self.render(frame, view)
            step = self.total_steps
            self.strategy.step_pre_backward(
                p, self.optimizers, self.strategy_state, step, info
            )
            loss = 0.8 * (image - target).abs().mean() + 0.2 * (
                1 - self.ssim(image, target)
            )
            loss += 1e-4 * p["scales"].exp().mean()
            loss += 1e-3 * max(0, 1 - step / 3000) * p["opacities"].sigmoid().mean()
            loss.backward()
            self.optimizers["means"].param_groups[0]["lr"] = self.rates[
                "means"
            ] * 0.01 ** min(step / 12000, 1)
            for optimizer in self.optimizers.values():
                optimizer.step()
                optimizer.zero_grad(set_to_none=True)
            if len(p["means"]) >= 125_000:
                self.strategy.refine_stop_iter = min(
                    self.strategy.refine_stop_iter, step
                )
            self.strategy.step_post_backward(
                p, self.optimizers, self.strategy_state, step, info, packed=False
            )
            with torch.no_grad():
                p["scales"].clamp_(-12, 2)
                p["opacities"].clamp_(-12, 12)
                p["colors"].clamp_(-12, 12)
                p["velocities"].clamp_(-100, 100)
                p["centre_times"].clamp_(0, self.duration)
                p["log_sigmas"].clamp_(
                    math.log(0.5 / self.rig.fps), math.log(4 * self.duration)
                )
            if len(p["means"]) > 250_000:
                raise RuntimeError("Live Gaussian safety budget exceeded")
            self.total_steps += 1
        self._sync()
        value = float(loss.detach())
        if not math.isfinite(value):
            raise RuntimeError("Nonfinite live training loss")
        return {
            "training_seconds": time.monotonic() - began,
            "update_steps": steps,
            "total_optimizer_steps": self.total_steps,
            "loss": value,
            "training_frame_sample_counts": sampled.tolist(),
            "training_source_frames": [f.number for f in window.frames],
            "optimization_pixels_latest_input_time_seconds": window.end,
            "training_views": self.rig.train.tolist(),
            "heldout_views": self.rig.heldout.tolist(),
        }

    def snapshot(self, window, deadline):
        torch, p = self.torch, self.params
        began = time.monotonic()
        with torch.no_grad():
            arrays = tuple(
                value.detach().cpu().numpy()
                for value in (
                    p["means"],
                    p["scales"].exp(),
                    p["quats"],
                    p["colors"].sigmoid(),
                    p["opacities"].sigmoid(),
                    p["velocities"],
                    p["centre_times"],
                    p["log_sigmas"].exp(),
                )
            )
            raw = pack_temporal(
                *arrays, frame_count=len(window.frames), frame_rate=self.rig.fps
            )
            payload = compress_temporal(raw)
            encoding_seconds = time.monotonic() - began
            stored = tuple(self.tensor(value) for value in unpack_temporal(payload))
            self.last_stored = stored
            began = time.monotonic()
            evaluations, first_renders, temporal_deltas = [], {}, []
            for frame in range(len(window.frames)):
                for view in self.rig.heldout:
                    if time.monotonic() >= deadline:
                        raise TimeoutError(
                            "Finite live-worker evaluation deadline exceeded"
                        )
                    view = int(view)
                    target = self.images[frame, view : view + 1].float() / 255
                    floating = self.render(frame, view)[0].clamp(0, 1)
                    packed = self.render(frame, view, stored)[0].clamp(0, 1)
                    if frame == 0:
                        first_renders[view] = packed.clone()
                    elif frame == len(window.frames) - 1:
                        temporal_deltas.append(
                            float((packed - first_renders[view]).abs().mean())
                        )
                    evaluations.append(
                        {
                            "source_frame": window.frames[frame].number,
                            "view": view,
                            "float_psnr": -10
                            * math.log10(
                                max(float((floating - target).square().mean()), 1e-12)
                            ),
                            "float_ssim": float(self.ssim(floating, target)),
                            "packed_psnr": -10
                            * math.log10(
                                max(float((packed - target).square().mean()), 1e-12)
                            ),
                            "packed_ssim": float(self.ssim(packed, target)),
                        }
                    )
            self._sync()
            metrics = {
                "mean_packed_psnr": float(
                    np.mean([row["packed_psnr"] for row in evaluations])
                ),
                "mean_packed_ssim": float(
                    np.mean([row["packed_ssim"] for row in evaluations])
                ),
                "mean_float_psnr": float(
                    np.mean([row["float_psnr"] for row in evaluations])
                ),
                "mean_float_ssim": float(
                    np.mean([row["float_ssim"] for row in evaluations])
                ),
                "evaluated_images": len(evaluations),
                "evaluations": evaluations,
                "evaluation_seconds": time.monotonic() - began,
                "encoding_seconds": encoding_seconds,
                "raw_model_bytes": len(raw),
                "model_bytes": len(payload),
                "stored_gaussians": len(arrays[0]),
                "initial_gaussians": self.initial_count,
                "nonzero_motion_gaussians_at_1e_minus_6": int(
                    np.count_nonzero(np.linalg.norm(arrays[5], axis=1) > 1e-6)
                ),
                "first_last_packed_render_mae": float(np.mean(temporal_deltas)),
                "seed": 0,
                "frame_rate": self.rig.fps,
                "frame_count": len(window.frames),
                "width": self.rig.width,
                "height": self.rig.height,
                "window_start_seconds": window.start,
                "window_end_seconds": window.end,
                "lifetime_policy": "local lifetime phase reset; means advanced by learned velocity times window shift; same Adam state",
                "torch_peak_allocated_bytes": torch.cuda.max_memory_allocated()
                if self.device == "cuda"
                else 0,
                "evaluation_caveat": "Held-out pixels excluded from all optimization; supplied calibration/seed geometry may contain held-out camera and temporal information.",
            }
        return payload, metrics

    def write_diagnostics(self, root, window):
        """Save three packed held-out RGB renders locally, after publication timing."""
        import io

        from PIL import Image

        from .output import immutable_file

        root = Path(root)
        root.mkdir(exist_ok=False)
        view = int(self.rig.heldout[0])
        with self.torch.no_grad():
            for frame in (0, len(window.frames) // 2, len(window.frames) - 1):
                predicted = self.render(frame, view, self.last_stored)[0][0].clamp(0, 1)
                rgb = (predicted * 255).round().to(self.torch.uint8).cpu().numpy()
                image = io.BytesIO()
                Image.fromarray(rgb).save(image, format="WEBP", quality=90)
                immutable_file(
                    root
                    / f"heldout-{view:02d}-frame-{window.frames[frame].number:06d}.webp",
                    image.getvalue(),
                )
