"""Jointly learn linear-motion, temporally gated RGB Gaussian primitives.

The conditional 4D Gaussian follows the viewer's existing RGS1 equations. This
is a small clean implementation inspired by FreeTimeGS, not its reproduction.
"""

import math
import time

import numpy as np

from .data import training_activity
from .packing import TEMPORAL, pack_temporal, unpack_temporal
from .packing import compress_temporal_smallest as compress_temporal


def temporal_values(params, time_seconds):
    """Differentiable position and opacity, in seconds and world units."""
    delta = time_seconds - params["centre_times"]
    means = params["means"] + params["velocities"] * delta[:, None]
    ratio = delta / params["log_sigmas"].exp()
    opacity = params["opacities"].sigmoid() * (-0.5 * ratio.square()).exp()
    return means, opacity


def stored_values(arrays, time_seconds):
    """Evaluate float32 RGS1 with the shader's roundEven RGBA8 alpha."""
    import torch

    means, scales, quats, colors, peak, velocity, centre, sigma = arrays
    delta = time_seconds - centre
    opacity = peak * (-0.5 * (delta / sigma).square()).exp()
    # GLSL roundEven and Torch round both use ties-to-even.
    opacity = torch.round(opacity * 255).clamp(0, 255) / 255
    return means + velocity * delta[:, None], scales, quats, colors, opacity


QUALITY_PROFILES = ("baseline", "motion-prior", "local-lifetime")


def initialize_parameters(
    xyz, rgb, spacing, frame_times, device="cuda", *, quality_profile="baseline"
):
    if quality_profile not in QUALITY_PROFILES:
        raise ValueError("Invalid quality profile")
    import torch

    tensor = lambda value: torch.as_tensor(value, dtype=torch.float32, device=device)
    count = len(xyz)
    duration = float(frame_times[-1])
    colors = tensor(rgb / 255).clamp(0.001, 0.999)
    quats = torch.zeros((count, 4), device=device)
    quats[:, 0] = 1
    params = torch.nn.ParameterDict(
        {
            "means": torch.nn.Parameter(tensor(xyz)),
            "scales": torch.nn.Parameter(
                tensor(np.log(spacing)[:, None].repeat(3, axis=1))
            ),
            "quats": torch.nn.Parameter(quats),
            "opacities": torch.nn.Parameter(
                torch.full((count,), math.log(0.1 / 0.9), device=device)
            ),
            "colors": torch.nn.Parameter(torch.logit(colors)),
            "velocities": torch.nn.Parameter(torch.zeros((count, 3), device=device)),
            "centre_times": torch.nn.Parameter(
                torch.full((count,), duration / 2, device=device)
            ),
            "log_sigmas": torch.nn.Parameter(
                torch.full((count,), math.log(duration), device=device)
            ),
        }
    )
    if quality_profile == "local-lifetime":
        # The existing shuffled seed-point order distributes times spatially,
        # with the same primitive count and no additional RNG draws.
        with torch.no_grad():
            params["centre_times"].copy_(
                torch.linspace(0, duration, count, device=device)
            )
            params["log_sigmas"].fill_(math.log(duration / 4))
    rates = {
        "means": 0.00016,
        "scales": 0.005,
        "quats": 0.001,
        "opacities": 0.05,
        "colors": 0.0025,
        "velocities": 0.001,
        "centre_times": 0.0005 * duration,
        "log_sigmas": 0.001,
    }
    # Every trainable primitive array must participate in topology mutations.
    optimizers = {
        key: torch.optim.Adam([param], lr=rates[key], eps=1e-15)
        for key, param in params.items()
    }
    return params, optimizers, rates


def motion_prior_loss(params, time_seconds, viewmat, K, activity, duration):
    """Penalize velocity at low-variation image locations, without moving means.

    Projection and the activity weights are detached. Only velocities receive
    this gradient; topology changes need no separate per-primitive metadata.
    """
    import torch
    import torch.nn.functional as F

    with torch.no_grad():
        means, _ = temporal_values(params, time_seconds)
        camera = means @ viewmat[:3, :3].T + viewmat[:3, 3]
        projected = camera @ K.T
        pixels = projected[:, :2] / projected[:, 2:3].clamp_min(1e-6)
        height, width = activity.shape
        valid = (
            (camera[:, 2] > 0)
            & (pixels[:, 0] >= 0)
            & (pixels[:, 0] <= width - 1)
            & (pixels[:, 1] >= 0)
            & (pixels[:, 1] <= height - 1)
        )
        grid = pixels / pixels.new_tensor([width - 1, height - 1]) * 2 - 1
        sampled = F.grid_sample(
            activity[None, None],
            grid[None, :, None],
            mode="bilinear",
            align_corners=True,
        ).flatten()
        weights = (1 - sampled).square() * valid
    displacement = params["velocities"] * duration
    return 0.01 * (displacement.square().sum(dim=1) * weights).mean()


def compression_candidate_models(raw):
    """Measure two float16 round-trip candidates without modifying the export.

    This manipulates only RGS1 float fields, preserving RGBA/quaternion bytes,
    header, primitive count, and canonical row order. Candidate bytes stay local
    to evaluation and are never returned as the worker's published model.
    """
    candidates = []
    for name, fields in (
        (
            "fp16-all-floats",
            ("position", "scale", "velocity", "centre_time", "sigma", "peak"),
        ),
        (
            "fp32-position-fp16-other",
            ("scale", "velocity", "centre_time", "sigma", "peak"),
        ),
    ):
        candidate = bytearray(raw)
        rows = np.frombuffer(candidate, dtype=TEMPORAL, offset=32)
        with np.errstate(over="ignore", under="ignore", invalid="ignore"):
            for field in fields:
                rows[field] = rows[field].astype(np.float16).astype(np.float32)
        metadata = {
            "name": name,
            "float16_fields": list(fields),
            "published": False,
            "primitive_ids_preserved": True,
            "raw_bytes": len(raw),
        }
        try:
            arrays = unpack_temporal(bytes(candidate))
            metadata.update(
                status="evaluated",
                compressed_bytes=len(compress_temporal(bytes(candidate))),
            )
        except ValueError as error:
            metadata.update(status="rejected", reason=str(error))
            arrays = None
        candidates.append((metadata, arrays))
    return candidates


def _ssim_function(device):
    import torch
    import torch.nn.functional as F

    axis = torch.arange(11, device=device, dtype=torch.float32) - 5
    gaussian = torch.exp(-axis.square() / (2 * 1.5**2))
    gaussian /= gaussian.sum()
    kernel = (gaussian[:, None] * gaussian[None, :]).expand(3, 1, 11, 11).contiguous()

    def ssim(a, b):
        a, b = a.permute(0, 3, 1, 2), b.permute(0, 3, 1, 2)
        mean_a, mean_b = F.conv2d(a, kernel, groups=3), F.conv2d(b, kernel, groups=3)
        var_a = F.conv2d(a * a, kernel, groups=3) - mean_a.square()
        var_b = F.conv2d(b * b, kernel, groups=3) - mean_b.square()
        cov = F.conv2d(a * b, kernel, groups=3) - mean_a * mean_b
        return (
            ((2 * mean_a * mean_b + 0.01**2) * (2 * cov + 0.03**2))
            / (
                (mean_a.square() + mean_b.square() + 0.01**2)
                * (var_a + var_b + 0.03**2)
            )
        ).mean()

    return ssim


def optimize_dynamic(
    data,
    steps,
    deadline,
    directory,
    *,
    quality_profile="baseline",
    evaluate_compression=False,
):
    from importlib.metadata import version

    import torch
    from gsplat import DefaultStrategy, rasterization
    from PIL import Image
    from scipy.spatial import cKDTree

    from .train import log

    if quality_profile not in QUALITY_PROFILES:
        raise ValueError("Invalid quality profile")
    if data.frame_times is None:
        raise ValueError("Dynamic training requires temporal input")
    if not torch.cuda.is_available():
        raise RuntimeError("A CUDA GPU is required")
    torch.manual_seed(0)
    torch.set_num_threads(4)
    rng = np.random.default_rng(0)
    selected = rng.choice(len(data.xyz), min(100_000, len(data.xyz)), replace=False)
    xyz = data.xyz[selected].astype(np.float32)
    distances = cKDTree(xyz).query(xyz, k=4, workers=1)[0][:, 1:]
    spacing = np.sqrt(np.mean(distances**2, axis=1)).clip(1e-4, 1)
    params, optimizers, rates = initialize_parameters(
        xyz,
        data.rgb[selected],
        spacing,
        data.frame_times,
        quality_profile=quality_profile,
    )
    strategy = DefaultStrategy(refine_stop_iter=min(6000, steps))
    strategy.check_sanity(params, optimizers)
    state = strategy.initialize_state(scene_scale=1.0)
    tensor = lambda value: torch.as_tensor(value, dtype=torch.float32, device="cuda")
    views, Ks, times = tensor(data.viewmats), tensor(data.Ks), tensor(data.frame_times)
    # Keep the complete video uint8; materialize only the selected float target.
    images = torch.as_tensor(data.images, dtype=torch.uint8, device="cuda")
    frames, _, height, width, _ = data.images.shape
    duration, fps = float(data.frame_times[-1]), data.frame_rate
    ssim = _ssim_function("cuda")
    quality_started = time.perf_counter()
    activity = (
        {index: tensor(value) for index, value in training_activity(data).items()}
        if quality_profile == "motion-prior"
        else {}
    )
    torch.cuda.synchronize()
    quality_setup_seconds = time.perf_counter() - quality_started

    def target(frame, index):
        return images[frame, index : index + 1].float() / 255

    def render(frame, index, arrays=None):
        if arrays is None:
            means, opacity = temporal_values(params, times[frame])
            values = (
                means,
                params["scales"].exp(),
                params["quats"],
                params["colors"].sigmoid(),
                opacity,
            )
        else:
            values = stored_values(arrays, times[frame])
        means, scales, quats, colors, opacity = values
        return rasterization(
            means=means,
            scales=scales,
            quats=quats,
            colors=colors,
            opacities=opacity,
            viewmats=views[index : index + 1],
            Ks=Ks[index : index + 1],
            width=width,
            height=height,
            packed=False,
        )

    torch.cuda.reset_peak_memory_stats()
    torch.cuda.synchronize()
    started = time.perf_counter()
    sampled_frames = np.zeros(frames, dtype=np.int64)
    for step in range(steps):
        if time.time() >= deadline - 90:
            raise TimeoutError("Training deadline reached; output was not published")
        frame = int(rng.integers(frames))
        index = int(rng.choice(data.train_indices))
        sampled_frames[frame] += 1
        image, _, info = render(frame, index)
        strategy.step_pre_backward(params, optimizers, state, step, info)
        truth = target(frame, index)
        loss = 0.8 * (image - truth).abs().mean() + 0.2 * (1 - ssim(image, truth))
        loss = loss + 1e-4 * params["scales"].exp().mean()
        # Discourage early opacity saturation while all timestamps begin to fit.
        opacity_weight = 1e-3 * max(0.0, 1 - step / max(1, 0.25 * steps))
        loss = loss + opacity_weight * params["opacities"].sigmoid().mean()
        if quality_profile == "motion-prior":
            loss = loss + motion_prior_loss(
                params, times[frame], views[index], Ks[index], activity[index], duration
            )
        loss.backward()
        optimizers["means"].param_groups[0]["lr"] = rates["means"] * 0.01 ** (
            step / steps
        )
        for optimizer in optimizers.values():
            optimizer.step()
            optimizer.zero_grad(set_to_none=True)
        if len(params["means"]) >= 250_000:
            strategy.refine_stop_iter = min(strategy.refine_stop_iter, step)
        strategy.step_post_backward(params, optimizers, state, step, info, packed=False)
        with torch.no_grad():
            params["scales"].clamp_(-12, 2)
            params["opacities"].clamp_(-12, 12)
            params["colors"].clamp_(-12, 12)
            params["velocities"].clamp_(-100, 100)
            params["centre_times"].clamp_(-duration, 2 * duration)
            params["log_sigmas"].clamp_(math.log(0.5 / fps), math.log(4 * duration))
        if len(params["means"]) > 500_000:
            raise RuntimeError("Gaussian budget exceeded")
        if step % 100 == 0:
            value = float(loss.detach())
            if not math.isfinite(value):
                raise RuntimeError("Nonfinite training loss")
            log(
                "training_progress",
                mode="dynamic",
                quality_profile=quality_profile,
                iteration=step,
                iterations=steps,
                frame=frame,
                time_seconds=float(data.frame_times[frame]),
                loss=value,
                gaussians=len(params["means"]),
                elapsed_seconds=time.perf_counter() - started,
            )
    torch.cuda.synchronize()
    training_seconds = time.perf_counter() - started
    evaluation_started = time.perf_counter()
    with torch.no_grad():
        arrays = tuple(
            value.detach().cpu().numpy()
            for value in (
                params["means"],
                params["scales"].exp(),
                params["quats"],
                params["colors"].sigmoid(),
                params["opacities"].sigmoid(),
                params["velocities"],
                params["centre_times"],
                params["log_sigmas"].exp(),
            )
        )
        raw = pack_temporal(*arrays, frame_count=frames, frame_rate=fps)
        payload = compress_temporal(raw)
        stored = tuple(tensor(array) for array in unpack_temporal(payload))
        compression_started = time.perf_counter()
        compression_candidates = []
        if evaluate_compression:
            for metadata, candidate_arrays in compression_candidate_models(raw):
                if candidate_arrays is not None:
                    metadata["evaluations"] = []
                    candidate_arrays = tuple(
                        tensor(array) for array in candidate_arrays
                    )
                compression_candidates.append((metadata, candidate_arrays))
        compression_seconds = time.perf_counter() - compression_started
        evaluations, first_renders, last_differences = [], {}, []
        diagnostics = {0, frames // 2, frames - 1}
        for frame in range(frames):
            for index in data.heldout_indices:
                if time.time() >= deadline - 10:
                    raise TimeoutError(
                        "Evaluation deadline reached; output was not published"
                    )
                index = int(index)
                image = render(frame, index)[0].clamp(0, 1)
                exported = render(frame, index, stored)[0].clamp(0, 1)
                truth = target(frame, index)
                reversed_truth = target(frames - 1 - frame, index)
                evaluations.append(
                    {
                        "frame": frame,
                        "time_seconds": float(data.frame_times[frame]),
                        "view": index,
                        "psnr": -10
                        * math.log10(
                            max(float((image - truth).square().mean()), 1e-12)
                        ),
                        "ssim": float(ssim(image, truth)),
                        "packed_psnr": -10
                        * math.log10(
                            max(float((exported - truth).square().mean()), 1e-12)
                        ),
                        "packed_ssim": float(ssim(exported, truth)),
                        "reversed_time_psnr": -10
                        * math.log10(
                            max(float((image - reversed_truth).square().mean()), 1e-12)
                        ),
                        "packed_reversed_time_psnr": -10
                        * math.log10(
                            max(
                                float((exported - reversed_truth).square().mean()),
                                1e-12,
                            )
                        ),
                    }
                )
                if evaluate_compression:
                    compression_started = time.perf_counter()
                    for metadata, candidate_arrays in compression_candidates:
                        if candidate_arrays is None:
                            continue
                        candidate_image = render(frame, index, candidate_arrays)[
                            0
                        ].clamp(0, 1)
                        difference = (candidate_image - exported).abs()
                        metadata["evaluations"].append(
                            {
                                "frame": frame,
                                "view": index,
                                "psnr": -10
                                * math.log10(
                                    max(
                                        float(
                                            (candidate_image - truth).square().mean()
                                        ),
                                        1e-12,
                                    )
                                ),
                                "ssim": float(ssim(candidate_image, truth)),
                                "reference_wire_mae": float(difference.mean()),
                                "reference_wire_max_difference": float(
                                    difference.max()
                                ),
                            }
                        )
                        if frame in diagnostics:
                            triptych = torch.cat(
                                (truth[0], exported[0], candidate_image[0]), dim=1
                            )
                            pixels = (
                                (triptych.cpu().numpy() * 255).round().astype(np.uint8)
                            )
                            Image.fromarray(pixels).save(
                                directory
                                / f"heldout-{index}-frame-{frame}-compression-{metadata['name']}.webp",
                                quality=90,
                            )
                    compression_seconds += time.perf_counter() - compression_started
                if frame == 0:
                    first_renders[index] = exported.clone()
                if frame == frames - 1:
                    last_differences.append(
                        float((exported - first_renders[index]).abs().mean())
                    )
                if frame in diagnostics:
                    pixels = (exported[0].cpu().numpy() * 255).round().astype(np.uint8)
                    Image.fromarray(pixels).save(
                        directory / f"heldout-{index}-frame-{frame}.webp", quality=90
                    )
        for metadata, _ in compression_candidates:
            if "evaluations" in metadata:
                rows = metadata["evaluations"]
                metadata.update(
                    evaluated_images=len(rows),
                    mean_psnr=float(np.mean([row["psnr"] for row in rows])),
                    mean_ssim=float(np.mean([row["ssim"] for row in rows])),
                    mean_reference_wire_mae=float(
                        np.mean([row["reference_wire_mae"] for row in rows])
                    ),
                    max_reference_wire_difference=max(
                        row["reference_wire_max_difference"] for row in rows
                    ),
                )
        speed = np.linalg.norm(arrays[5], axis=1)
        first_values = stored_values(stored, times[0])
        last_values = stored_values(stored, times[-1])
        first_means, first_alpha = first_values[0], first_values[4]
        last_means, last_alpha = last_values[0], last_values[4]
        evaluation_seconds = time.perf_counter() - evaluation_started
    return payload, {
        "mode": "dynamic",
        "quality_profile": quality_profile,
        "compression_evaluation": {
            "enabled": evaluate_compression,
            "seconds": compression_seconds,
            "reference": "original exported wire; candidate quantization is evaluated, never adopted",
            "diagnostic_columns": ["ground truth", "original wire", "candidate"],
            "candidates": [metadata for metadata, _ in compression_candidates],
        },
        "quality_setup_seconds": quality_setup_seconds,
        "quality_configuration": {
            "initial_count_multiplier": 1,
            "initial_sigma_duration_fraction": 0.25
            if quality_profile == "local-lifetime"
            else 1.0,
            "initial_centres": "uniform interval, existing shuffled point order"
            if quality_profile == "local-lifetime"
            else "interval midpoint",
            "motion_prior_weight": 0.01 if quality_profile == "motion-prior" else 0,
            "activity_std_rgb_threshold": 0.05
            if quality_profile == "motion-prior"
            else None,
            "activity_views": data.train_indices.tolist() if activity else [],
            "hypothesis_only": True,
        },
        "seed": 0,
        "iterations": steps,
        "frame_count": frames,
        "frame_rate": fps,
        "duration_seconds": duration,
        "frame_times_seconds": data.frame_times.tolist(),
        "training_seconds": training_seconds,
        "evaluation_seconds": evaluation_seconds,
        "training_views": data.train_indices.tolist(),
        "heldout_views": data.heldout_indices.tolist(),
        "width": width,
        "height": height,
        "initial_gaussians": len(xyz),
        "stored_gaussians": len(arrays[0]),
        "evaluated_images": len(evaluations),
        "evaluations": evaluations,
        "mean_psnr": float(np.mean([row["psnr"] for row in evaluations])),
        "mean_ssim": float(np.mean([row["ssim"] for row in evaluations])),
        "mean_packed_psnr": float(np.mean([row["packed_psnr"] for row in evaluations])),
        "mean_packed_ssim": float(np.mean([row["packed_ssim"] for row in evaluations])),
        "temporal_evidence": {
            "training_samples_per_frame": sampled_frames.tolist(),
            "matched_time_mean_psnr": float(
                np.mean([row["psnr"] for row in evaluations])
            ),
            "reversed_time_mean_psnr": float(
                np.mean([row["reversed_time_psnr"] for row in evaluations])
            ),
            "matched_time_mean_packed_psnr": float(
                np.mean([row["packed_psnr"] for row in evaluations])
            ),
            "reversed_time_mean_packed_psnr": float(
                np.mean([row["packed_reversed_time_psnr"] for row in evaluations])
            ),
            "time_correspondence_diagnostic": "Same held-out renders compared with target[t] and target[T-1-t], same camera. Full image, no motion ROI; background can dominate.",
            "nonzero_motion_gaussians": int(np.count_nonzero(speed > 1e-6)),
            "mean_speed_world_units_per_second": float(speed.mean()),
            "max_speed_world_units_per_second": float(speed.max()),
            "mean_first_last_displacement": float(
                (last_means - first_means).norm(dim=1).mean()
            ),
            "mean_first_last_opacity_difference": float(
                (last_alpha - first_alpha).abs().mean()
            ),
            "heldout_first_last_render_mae": last_differences,
        },
        "torch_peak_allocated_bytes": torch.cuda.max_memory_allocated(),
        "torch_version": torch.__version__,
        "gsplat_version": version("gsplat"),
        "gpu": torch.cuda.get_device_name(),
        "model": "joint linear-motion temporal RGB Gaussian, gsplat DefaultStrategy, 0.8 L1 + 0.2 SSIM + 1e-4 mean spatial scale + initial decaying 1e-3 mean peak opacity",
        "reference": "https://arxiv.org/html/2506.05348v1 (representation inspiration; not an official reproduction)",
        "evaluation_caveat": "Held-out pixels at every time are excluded from training. Supplied initial geometry/calibration may use all source cameras.",
        "packing": "RGS1: float32 geometry/motion/peak, RGB8/quaternion8; RGBA8 roundEven opacity evaluated per time, lossless byte shuffle + gzip, unchanged primitive IDs",
        "raw_model_bytes": len(raw),
    }
