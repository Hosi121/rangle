"""Run one bounded static or dynamic Gaussian reconstruction and publish its preview."""

import argparse
import gzip
import hashlib
import json
import math
import os
import re
import tempfile
import threading
import time
from datetime import datetime
from pathlib import Path
from urllib.parse import urlsplit

import numpy as np

from .data import load_dataset, scene_camera
from .packing import pack_splats, unpack_splats


def log(event, severity="INFO", **values):
    print(
        json.dumps(
            {
                "service": "rangle-trainer",
                "severity": severity,
                "event": event,
                **values,
            },
            allow_nan=False,
        ),
        flush=True,
    )


def uri(value):
    parsed = urlsplit(value)
    if (
        parsed.scheme != "gs"
        or not parsed.netloc
        or parsed.username
        or parsed.query
        or parsed.fragment
        or ".." in parsed.path.split("/")
        or not parsed.path.strip("/")
    ):
        raise ValueError("Expected an explicit gs://bucket/object path")
    return parsed.netloc, parsed.path.lstrip("/")


def optimize(data, steps, deadline, directory):
    # CUDA imports are confined to actual training; contract/packing tests use CPU only.
    import torch
    import torch.nn.functional as F
    from gsplat import DefaultStrategy, rasterization
    from PIL import Image
    from scipy.spatial import cKDTree

    if not torch.cuda.is_available():
        raise RuntimeError("A CUDA GPU is required")
    torch.manual_seed(0)
    torch.set_num_threads(4)
    rng = np.random.default_rng(0)
    selected = rng.choice(len(data.xyz), min(100_000, len(data.xyz)), replace=False)
    xyz = data.xyz[selected].astype(np.float32)
    distances = cKDTree(xyz).query(xyz, k=4, workers=1)[0][:, 1:]
    spacing = np.sqrt(np.mean(distances**2, axis=1)).clip(1e-4, 1)
    tensor = lambda value: torch.as_tensor(value, dtype=torch.float32, device="cuda")
    colors = tensor(data.rgb[selected] / 255).clamp(0.001, 0.999)
    quats = torch.zeros((len(xyz), 4), device="cuda")
    quats[:, 0] = 1
    params = torch.nn.ParameterDict(
        {
            "means": torch.nn.Parameter(tensor(xyz)),
            "scales": torch.nn.Parameter(
                tensor(np.log(spacing)[:, None].repeat(3, axis=1))
            ),
            "quats": torch.nn.Parameter(quats),
            "opacities": torch.nn.Parameter(
                torch.full((len(xyz),), math.log(0.1 / 0.9), device="cuda")
            ),
            "colors": torch.nn.Parameter(torch.logit(colors)),
        }
    )
    rates = {
        "means": 0.00016,
        "scales": 0.005,
        "quats": 0.001,
        "opacities": 0.05,
        "colors": 0.0025,
    }
    optimizers = {
        key: torch.optim.Adam([param], lr=rates[key], eps=1e-15)
        for key, param in params.items()
    }
    strategy = DefaultStrategy(refine_stop_iter=min(4000, steps))
    strategy.check_sanity(params, optimizers)
    state = strategy.initialize_state(scene_scale=1.0)
    targets, views, Ks = (
        tensor(data.images) / 255,
        tensor(data.viewmats),
        tensor(data.Ks),
    )
    height, width = data.images.shape[1:3]
    axis = torch.arange(11, device="cuda", dtype=torch.float32) - 5
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

    def render(index, arrays=None):
        if arrays is None:
            values = (
                params["means"],
                params["scales"].exp(),
                params["quats"],
                params["colors"].sigmoid(),
                params["opacities"].sigmoid(),
            )
        else:
            values = tuple(tensor(array) for array in arrays)
        means, scales, rotations, rgb, opacity = values
        return rasterization(
            means=means,
            scales=scales,
            quats=rotations,
            colors=rgb,
            opacities=opacity,
            viewmats=views[index : index + 1],
            Ks=Ks[index : index + 1],
            width=width,
            height=height,
            packed=False,
        )

    torch.cuda.reset_peak_memory_stats()
    started = time.perf_counter()
    for step in range(steps):
        if time.time() >= deadline - 30:
            raise TimeoutError("Training deadline reached; output was not published")
        index = int(rng.choice(data.train_indices))
        image, _, info = render(index)
        strategy.step_pre_backward(params, optimizers, state, step, info)
        loss = 0.8 * (image - targets[index : index + 1]).abs().mean() + 0.2 * (
            1 - ssim(image, targets[index : index + 1])
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
        if len(params["means"]) > 500_000:
            raise RuntimeError("Gaussian budget exceeded")
        if step % 100 == 0:
            value = float(loss.detach())
            if not math.isfinite(value):
                raise RuntimeError("Nonfinite training loss")
            log(
                "training_progress",
                iteration=step,
                iterations=steps,
                loss=value,
                gaussians=len(params["means"]),
                elapsed_seconds=time.perf_counter() - started,
            )
    torch.cuda.synchronize()
    training_seconds = time.perf_counter() - started
    with torch.no_grad():
        arrays = tuple(
            value.detach().cpu().numpy()
            for value in (
                params["means"],
                params["scales"].exp(),
                params["quats"],
                params["colors"].sigmoid(),
                params["opacities"].sigmoid(),
            )
        )
        payload = pack_splats(*arrays)
        if not payload:
            raise RuntimeError("Training produced no visible Gaussians")
        packed = unpack_splats(payload)
        evaluations = []
        for index in data.heldout_indices:
            index = int(index)
            image = render(index)[0].clamp(0, 1)
            stored = render(index, packed)[0].clamp(0, 1)
            target = targets[index : index + 1]
            mse = float((image - target).square().mean())
            packed_mse = float((stored - target).square().mean())
            evaluations.append(
                {
                    "view": index,
                    "psnr": -10 * math.log10(max(mse, 1e-12)),
                    "ssim": float(ssim(image, target)),
                    "packed_psnr": -10 * math.log10(max(packed_mse, 1e-12)),
                }
            )
            pixels = (stored[0].cpu().numpy() * 255).round().astype(np.uint8)
            Image.fromarray(pixels).save(
                directory / f"heldout-{index}.webp", quality=90
            )
    return payload, {
        "seed": 0,
        "iterations": steps,
        "training_seconds": training_seconds,
        "training_views": data.train_indices.tolist(),
        "heldout_views": data.heldout_indices.tolist(),
        "width": width,
        "height": height,
        "initial_gaussians": len(xyz),
        "stored_gaussians": len(payload) // 32,
        "evaluations": evaluations,
        "torch_peak_allocated_bytes": torch.cuda.max_memory_allocated(),
        "torch_version": torch.__version__,
        "gpu": torch.cuda.get_device_name(),
        "model": "static RGB 3DGS, DefaultStrategy, 0.8 L1 + 0.2 SSIM",
        "evaluation_caveat": "Held-out pixels are excluded from training. Supplied initial geometry/calibration may use all source cameras.",
        "packing": "32-byte splat: float32 geometry, RGBA8, quaternion8; float and packed PSNR reported separately",
    }


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in (
        "input-uri",
        "input-sha256",
        "output-prefix",
        "job-id",
        "deadline",
        "title",
        "source",
        "license",
    ):
        parser.add_argument("--" + flag, required=True)
    parser.add_argument("--steps", type=int, default=6000)
    parser.add_argument("--mode", choices=("static", "dynamic"), default="static")
    parser.add_argument(
        "--quality-profile",
        choices=("baseline", "motion-prior", "local-lifetime"),
        default="baseline",
    )
    parser.add_argument("--evaluate-compression", action="store_true")
    args = parser.parse_args(argv)
    if (
        not re.fullmatch(r"[a-f0-9]{32}", args.job_id)
        or not re.fullmatch(r"[a-f0-9]{64}", args.input_sha256)
        or not 1 <= args.steps <= (12000 if args.mode == "dynamic" else 6000)
        or (
            args.mode != "dynamic"
            and (args.quality_profile != "baseline" or args.evaluate_compression)
        )
    ):
        raise ValueError("Invalid job identity, checksum or iteration budget")
    return args


def build_scene(args, dataset):
    scene = {
        "version": 1,
        "frameRate": dataset.frame_rate,
        "camera": scene_camera(dataset),
        "attribution": {
            "label": args.title,
            "source": args.source,
            "license": args.license,
        },
    }
    if args.mode == "dynamic":
        scene.update(frameCount=len(dataset.frame_times), temporal={"url": "model.rgs"})
    else:
        scene["frames"] = ["model.splat"]
    return scene


def main(*, storage_client_factory=None):
    args = parse_args()
    quality_profile = getattr(args, "quality_profile", "baseline")
    deadline = datetime.fromisoformat(args.deadline.replace("Z", "+00:00"))
    if deadline.tzinfo is None:
        raise ValueError("Deadline must include its timezone")
    expires = deadline.timestamp()
    remaining = expires - time.time()
    if not 0 < remaining <= 1801:
        raise TimeoutError("Job deadline is expired or exceeds thirty minutes")
    input_bucket, input_name = uri(args.input_uri)
    output_bucket, output_prefix = uri(args.output_prefix)
    if output_prefix != f"generated/{args.job_id}/":
        raise ValueError("Output must be scoped to this generation job")

    def expire():
        log("deadline_exceeded", severity="ERROR", job_id=args.job_id)
        os._exit(124)

    watchdog = threading.Timer(remaining, expire)
    watchdog.daemon = True
    watchdog.start()
    client = None
    try:
        if storage_client_factory is None:
            from google.cloud import storage

            storage_client_factory = storage.Client
        client = storage_client_factory()
        with tempfile.TemporaryDirectory(prefix="rangle-training-") as temporary:
            directory = Path(temporary)
            source = client.bucket(input_bucket).blob(input_name)
            source.reload(timeout=30)
            if source.size is None or source.size > 256 << 20:
                raise ValueError("Input download budget exceeded")
            dataset_file = directory / "dataset.npz"
            source.download_to_filename(
                dataset_file, if_generation_match=source.generation, timeout=60
            )
            with dataset_file.open("rb") as downloaded:
                if (
                    hashlib.file_digest(downloaded, "sha256").hexdigest()
                    != args.input_sha256
                ):
                    raise ValueError("Input checksum mismatch")
            dataset = load_dataset(dataset_file, mode=args.mode)
            log(
                "training_started",
                job_id=args.job_id,
                mode=args.mode,
                input_sha256=args.input_sha256,
            )
            if args.mode == "dynamic":
                from .temporal import optimize_dynamic

                quality_options = (
                    {"quality_profile": quality_profile}
                    if quality_profile != "baseline"
                    else {}
                )
                if getattr(args, "evaluate_compression", False):
                    quality_options["evaluate_compression"] = True
                payload, metrics = optimize_dynamic(
                    dataset, args.steps, expires, directory, **quality_options
                )
                model_name, model_bytes = "model.rgs", payload
            else:
                payload, metrics = optimize(dataset, args.steps, expires, directory)
                model_name, model_bytes = "model.splat", gzip.compress(payload, mtime=0)
            metrics.update(
                {
                    "job_id": args.job_id,
                    "mode": args.mode,
                    "quality_profile": quality_profile,
                    "input_sha256": args.input_sha256,
                    "model_bytes": len(model_bytes),
                    "model_sha256": hashlib.sha256(model_bytes).hexdigest(),
                    "source": args.source,
                    "license": args.license,
                }
            )
            if args.mode == "static":
                metrics.update(
                    splat_bytes=len(payload),
                    splat_sha256=hashlib.sha256(payload).hexdigest(),
                )
            (directory / model_name).write_bytes(model_bytes)
            (directory / "metrics.json").write_text(
                json.dumps(metrics, allow_nan=False, indent=2)
            )
            scene = build_scene(args, dataset)
            (directory / "scene.json").write_text(json.dumps(scene, allow_nan=False))
            bucket = client.bucket(output_bucket)
            # Publish the scene only after all referenced files are complete.
            files = [
                directory / model_name,
                directory / "metrics.json",
                *sorted(directory.glob("heldout-*.webp")),
                directory / "scene.json",
            ]
            for path in files:
                if time.time() >= expires - 5:
                    raise TimeoutError("Publication deadline reached")
                blob = bucket.blob(output_prefix + path.name)
                blob.cache_control = "public,max-age=31536000,immutable"
                if path.suffix == ".splat":
                    blob.content_encoding = "gzip"
                content_type = (
                    "application/json"
                    if path.suffix == ".json"
                    else "image/webp"
                    if path.suffix == ".webp"
                    else "application/octet-stream"
                )
                blob.upload_from_filename(
                    path, content_type=content_type, if_generation_match=0, timeout=60
                )
            log(
                "training_completed",
                job_id=args.job_id,
                mode=args.mode,
                training_seconds=metrics["training_seconds"],
                gaussians=metrics["stored_gaussians"],
            )
    finally:
        watchdog.cancel()
        if client is not None:
            client.close()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:  # noqa: BLE001 - structure failures once at the process boundary.
        log(
            "training_failed",
            severity="ERROR",
            error_type=type(error).__name__,
            message=str(error)[:500],
        )
        raise SystemExit(1) from None
