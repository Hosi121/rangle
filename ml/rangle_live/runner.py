"""One finite, paced source with persistent training and bounded pending work."""

import json
import threading
import time
from datetime import datetime, timezone
from pathlib import Path

from .model import IncrementalModel, bootstrap_cuda, warmup_cuda
from .output import Publisher, atomic_index, json_bytes
from .source import Rig, VideoDecoder, produce
from .window import LatestWindow


def log(event, severity="INFO", **values):
    print(
        json.dumps(
            {
                "service": "rangle-live",
                "severity": severity,
                "event": event,
                "time": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                **values,
            },
            allow_nan=False,
        ),
        flush=True,
    )


def run(args):
    process_started = time.monotonic()
    rig = Rig(args.source_manifest, args.source_root)
    frame_count = round(args.source_seconds * rig.fps)
    if (
        frame_count < rig.window_frames
        or (frame_count - 1) / rig.fps < args.seed_available_after
    ):
        raise ValueError(
            "Source duration must include a complete window and declared seed-prior time"
        )
    software = bootstrap_cuda(args.cuda_extension)
    if getattr(args, "cloud_warmup", False):
        warmup_cuda()
    decoder = VideoDecoder(rig, args.decode_workers)
    origin, origin_utc = time.monotonic(), datetime.now(timezone.utc)
    source = {
        "label": rig.config["source"]["name"],
        "url": rig.config["source"]["url"],
        "license": rig.config["source"]["license"],
        "caveat": "Recorded videos paced at original PTS. Precomputed author calibration/initial geometry may contain source-camera and temporal information; arrived-image optimization is causal, geometry is not claimed fully causal.",
    }
    try:
        publisher = Publisher(
            args.output_dir, args.session_id, args.title, source, origin, origin_utc
        )
    except BaseException:
        decoder.close()
        raise
    bus, stop = LatestWindow(), threading.Event()
    deadline = process_started + args.max_runtime_seconds

    last_status_write = float("-inf")

    def observe(status, force=False):
        nonlocal last_status_write
        if getattr(args, "status_path", None) and (
            force or time.monotonic() - last_status_write >= 1
        ):
            atomic_index(
                Path(args.status_path),
                json_bytes(
                    {
                        "version": 1,
                        "capture_origin_monotonic": origin,
                        "capture_started_at": origin_utc.isoformat().replace(
                            "+00:00", "Z"
                        ),
                        "startup_seconds": origin - process_started,
                        **status,
                    }
                ),
            )
            last_status_write = time.monotonic()
        publisher.observe(status, force)
        if force or status["frames_received"] % 50 == 0:
            log("source_progress", **status)

    producer = threading.Thread(
        target=produce,
        args=(
            decoder,
            bus,
            stop,
            origin,
            frame_count,
            rig.fps,
            rig.window_frames,
            args.seed_available_after,
            observe,
        ),
        name="live-arrived-source",
        daemon=False,
    )
    snapshots = []
    model = None
    log(
        "source_started",
        source_seconds=args.source_seconds,
        software=software,
        startup_seconds=origin - process_started,
        seed_available_after_seconds=args.seed_available_after,
    )
    producer.start()
    try:
        while True:
            window = bus.take(deadline)
            if window is None:
                break
            if model is None:
                rig.admit_seed(window.end, args.seed_available_after)
                model = IncrementalModel(rig, rig.window_frames)
                log("seed_prior_admitted", **rig.provenance)
            began = time.monotonic()
            steps = args.bootstrap_steps if not snapshots else args.steps_per_update
            log(
                "window_training_started",
                source_start=window.start,
                source_end=window.end,
                steps=steps,
            )
            metrics = model.fit(window, steps, deadline)
            last_window = window
            metrics.update(
                queue_wait_seconds=began - window.frames[-1].arrived_monotonic,
                decoder_profile=decoder.profile,
                provenance=rig.provenance,
                software=software,
                source_captured_at_clock="session UTC epoch + original source PTS; paced recorded-source simulation",
            )
            log(
                "window_training_completed",
                **{
                    key: metrics[key]
                    for key in ("training_seconds", "total_optimizer_steps", "loss")
                },
            )
            payload, evaluation = model.snapshot(window, deadline)
            metrics.update(evaluation)
            result = publisher.publish(
                payload, metrics, window, rig.camera, bus.status()
            )
            snapshots.append(
                {key: value for key, value in result.items() if key != "scene_url"}
            )
            log("snapshot_available", **result, **bus.status())
        producer.join()
        publisher.finish("ended", bus.status())
        if not snapshots:
            raise RuntimeError("The source produced no eligible arrived window")
    except BaseException:
        stop.set()
        producer.join(timeout=10)
        publisher.finish("failed", bus.status())
        raise
    finally:
        stop.set()
        producer.join(timeout=10)
    status = bus.status()
    summary = {
        "version": 1,
        "source_seconds": args.source_seconds,
        "bootstrap_steps": args.bootstrap_steps,
        "steps_per_update": args.steps_per_update,
        "window_frames": rig.window_frames,
        "software": software,
        "startup_seconds": origin - process_started,
        "total_wall_seconds": time.monotonic() - process_started,
        "first_snapshot_since_capture_start_seconds": publisher.first_available
        - origin,
        "first_snapshot_before_source_completion": publisher.first_available
        < status["source_finished_monotonic"],
        "snapshot_count": len(snapshots),
        "snapshots": snapshots,
        "source_status": status,
        "decoder_profile": decoder.profile,
        "mean_capture_to_local_snapshot_seconds": sum(
            s["capture_to_local_snapshot_seconds"] for s in snapshots
        )
        / len(snapshots),
        "max_capture_to_local_snapshot_seconds": max(
            s["capture_to_local_snapshot_seconds"] for s in snapshots
        ),
        "latency_scope": "capture PTS to local immutable scene/index availability; excludes cloud upload, HTTP delivery, client polling/decode/display",
        "geometry_prior_caveat": source["caveat"],
        "provenance": rig.provenance,
        "cloud_operations": False,
        "offline_trained_model_warm_start": False,
    }
    # The capture-to-index measurements above exclude these local-only images.
    model.write_diagnostics(publisher.root / "diagnostics", last_window)
    summary["diagnostics"] = (
        "diagnostics/heldout-*.webp; final packed snapshot, first held-out view, first/middle/last time; excluded from availability timing"
    )
    (publisher.root / "run-summary.json").write_bytes(json_bytes(summary))
    log(
        "live_run_complete",
        snapshots=len(snapshots),
        first_before_source_completion=summary[
            "first_snapshot_before_source_completion"
        ],
        mean_capture_to_local_snapshot_seconds=summary[
            "mean_capture_to_local_snapshot_seconds"
        ],
    )
    return summary
