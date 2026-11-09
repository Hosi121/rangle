"""Finite ADC cloud supervisor for incremental calibrated recorded-source replay."""

import argparse
import hashlib
import json
import math
import os
import re
import signal
import subprocess
import sys
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path
from types import SimpleNamespace
from urllib.parse import urlsplit

from .bundle import MAX_BYTES, extract
from .output import atomic_index, json_bytes
from .publishing import PublishError, adc_clients, protocol
from .runner import log
from .source import Rig


class Deadline:
    def __init__(self, timestamp):
        parsed = datetime.fromisoformat(timestamp.replace("Z", "+00:00"))
        if parsed.utcoffset() is None or parsed.utcoffset().total_seconds() != 0:
            raise ValueError("Deadline must use UTC")
        self.end = time.monotonic() + min(
            1800, (parsed - datetime.now(timezone.utc)).total_seconds()
        )
        self.remaining(1800)

    def remaining(self, maximum):
        value = min(maximum, self.end - time.monotonic())
        if value <= 0:
            raise TimeoutError("Cloud live deadline exceeded")
        return value


def gs_uri(value):
    parsed = urlsplit(value)
    if (
        parsed.scheme != "gs"
        or not re.fullmatch(r"[a-z0-9][a-z0-9._-]{1,221}[a-z0-9]", parsed.netloc)
        or parsed.query
        or parsed.fragment
        or parsed.username
        or "%" in parsed.path
        or "\\" in parsed.path
        or ".." in parsed.path.split("/")
        or "//" in parsed.path
    ):
        raise ValueError("Invalid fixed GCS URI")
    return parsed.netloc, parsed.path.removeprefix("/")


def parse_args(argv=None):
    cli = argparse.ArgumentParser(description=__doc__)
    for name in (
        "input-uri",
        "input-sha256",
        "output-prefix",
        "catalog-bucket",
        "media-origin",
        "session-id",
        "deadline",
        "title",
        "source",
        "license",
    ):
        cli.add_argument("--" + name, required=True)
    cli.add_argument("--source-seconds", type=float, default=10)
    cli.add_argument("--seed-available-after", type=float, default=0.8)
    cli.add_argument("--bootstrap-steps", type=int, default=300)
    cli.add_argument("--steps-per-update", type=int, default=300)
    cli.add_argument("--decode-workers", type=int, default=4)
    args = cli.parse_args(argv)
    args.input_bucket, args.input_key = gs_uri(args.input_uri)
    args.media_bucket, args.media_prefix = gs_uri(args.output_prefix)
    origin = urlsplit(args.media_origin)
    source = urlsplit(args.source)
    if (
        not re.fullmatch(r"[A-Za-z0-9_-]{1,64}", args.session_id)
        or not re.fullmatch(r"[0-9a-f]{64}", args.input_sha256)
        or not re.fullmatch(r"datasets/live/[A-Za-z0-9_.-]+\.zip", args.input_key)
        or args.media_prefix != f"live/{args.session_id}/"
        or len({args.input_bucket, args.media_bucket, args.catalog_bucket}) != 3
        or not re.fullmatch(r"[a-z0-9][a-z0-9._-]{1,221}[a-z0-9]", args.catalog_bucket)
        or origin.scheme != "https"
        or not origin.hostname
        or origin.netloc != origin.hostname
        or origin.path
        or origin.query
        or origin.fragment
        or source.scheme != "https"
        or not source.netloc
        or source.username
        or source.password
        or not args.title
        or len(args.title) > 256
        or args.license != "CC BY 4.0"
        or not 0 < args.source_seconds <= 1800
        or not 0 <= args.seed_available_after < args.source_seconds
        or not 1 <= args.bootstrap_steps <= 1000
        or not 1 <= args.steps_per_update <= 2000
        or not 1 <= args.decode_workers <= 32
    ):
        cli.error("Invalid approved live source, scope or finite training budget")
    Deadline(args.deadline)
    return args


class LimitedDownload:
    def __init__(self, target):
        self.target, self.count, self.digest = target, 0, hashlib.sha256()

    def write(self, data):
        self.count += len(data)
        if self.count > MAX_BYTES:
            raise ValueError("Source download exceeds 1 GiB")
        self.digest.update(data)
        return self.target.write(data)

    def flush(self):
        self.target.flush()


def download(client, args, path, deadline):
    try:
        return _download(client, args, path, deadline)
    except (ValueError, TimeoutError):
        raise
    except Exception as error:  # noqa: BLE001 - SDK HTTP/auth bodies never enter logs
        raise PublishError(
            f"Source storage read failed ({type(error).__name__})"
        ) from None


def _download(client, args, path, deadline):
    blob = client.bucket(args.input_bucket).blob(args.input_key)
    blob.reload(timeout=deadline.remaining(30), retry=None)
    if (
        blob.size is None
        or not 0 < blob.size <= MAX_BYTES
        or blob.content_encoding
        or not blob.generation
        or not str(blob.generation).isdigit()
    ):
        raise ValueError("Invalid bounded source object metadata")
    blob.chunk_size = 8 << 20
    with Path(path).open("xb") as target:
        writer = LimitedDownload(target)
        blob.download_to_file(
            writer,
            if_generation_match=int(blob.generation),
            timeout=deadline.remaining(90),
            retry=None,
            raw_download=True,
        )
        if writer.count != blob.size or writer.digest.hexdigest() != args.input_sha256:
            raise ValueError("Source download SHA256/size mismatch")
    return {
        "bytes": writer.count,
        "generation": str(blob.generation),
        "sha256": writer.digest.hexdigest(),
    }


def terminate_group(child, grace=2):
    forced = False
    if child.poll() is None:
        try:
            os.killpg(child.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            child.wait(timeout=grace)
        except subprocess.TimeoutExpired:
            forced = True
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            child.wait(timeout=grace)
    return {
        "exit_code": child.returncode,
        "forced_kill": forced,
        "reaped": child.poll() is not None,
    }


def read_json(path):
    return protocol.storage.json_bytes(json.loads(Path(path).read_bytes()))


def remote_verified(origin, key, expected, content_type, deadline):
    for attempt in range(4):
        try:
            return protocol.remote_file(origin, key, expected, content_type, deadline)
        except PublishError:
            if attempt == 3:
                raise
            time.sleep(deadline.remaining(0.5))


def publish(args, path, cloud, deadline, document=None):
    document = json.loads(read_json(path)) if document is None else document
    settings = SimpleNamespace(
        gate="/usr/local/bin/rangle-live-check",
        root=str(path.parent),
        origin=args.media_origin,
        approved_source=args.source,
        media_bucket=args.media_bucket,
        catalog_bucket=args.catalog_bucket,
        receipts=str(path.parent.parent / "receipts"),
    )
    # One finite call at a time; the next iteration reads the newest local index.
    for attempt in range(3):
        try:
            receipt = protocol.publish_once(
                settings, document, cloud, deadline, remote=remote_verified
            )
            break
        except PublishError as error:
            if "changed concurrently" not in str(error) or attempt == 2:
                raise
            time.sleep(deadline.remaining(0.2))
    if receipt["latest_sequence"]:
        public, _ = cloud.read_document(
            args.media_bucket, f"live-index/{args.session_id}.json"
        )
        remote_verified(
            args.media_origin,
            f"live-index/{args.session_id}.json",
            protocol.storage.json_bytes(public),
            "application/json",
            deadline,
        )
        confirmed = time.monotonic()
        status = json.loads((path.parent.parent / "source-status.json").read_bytes())
        origin = status["capture_origin_monotonic"]
        local = {
            segment["sequence"]: segment
            for segment in document["sessions"][0]["segments"]
        }
        evidence = []
        for segment in public["segments"]:
            if segment["sequence"] not in local:
                continue
            record = local[segment["sequence"]]
            metrics_path = (
                path.parent / record["scene_url"].lstrip("/")
            ).parent / "metrics.json"
            metrics = json.loads(metrics_path.read_bytes())
            evidence.append(
                {
                    "sequence": segment["sequence"],
                    "model_sha256": segment["model_sha256"],
                    "model_bytes": segment["model_bytes"],
                    "source_end_seconds": segment["end_seconds"],
                    "public_http_confirmed_monotonic": confirmed,
                    "capture_to_public_http_seconds": confirmed
                    - origin
                    - segment["end_seconds"],
                    **{
                        key: metrics[key]
                        for key in (
                            "mean_packed_psnr",
                            "mean_packed_ssim",
                            "mean_float_psnr",
                            "mean_float_ssim",
                            "stored_gaussians",
                            "training_seconds",
                            "encoding_seconds",
                            "evaluation_seconds",
                            "total_optimizer_steps",
                        )
                    },
                }
            )
        receipt["public_http_confirmed_monotonic"] = confirmed
        receipt["confirmed_segments"] = evidence
    return receipt


class LatestPublication:
    """Coalesce atomic local updates; no queue or training/source backpressure.

    GCS allows one write per second to the same object name. Wait 1.25s
    *after* a successful publication, including final/terminal publications.
    https://docs.cloud.google.com/storage/quotas#objects
    """

    def __init__(self, args, path, cloud, deadline):
        self.args, self.path, self.cloud, self.deadline = args, path, cloud, deadline
        self.seen, self.next_at = None, 0.0

    def poll(self, wait=False):
        if not self.path.exists():
            return None
        raw = read_json(self.path)
        digest = hashlib.sha256(raw).hexdigest()
        if digest == self.seen:
            return None
        delay = self.next_at - time.monotonic()
        if delay > 0:
            if not wait:
                return None
            if self.deadline.remaining(delay + 0.01) <= delay:
                raise TimeoutError("Publication pacing exceeds live deadline")
            time.sleep(delay)
            # A source update may have arrived while a final/terminal call waited.
            raw = read_json(self.path)
            digest = hashlib.sha256(raw).hexdigest()
        receipt = publish(
            self.args, self.path, self.cloud, self.deadline, json.loads(raw)
        )
        self.next_at = time.monotonic() + 1.25
        self.seen = digest
        return receipt


def supervise(args, client, cloud, deadline, root, started=None):
    started = time.monotonic() if started is None else started
    root = Path(root)
    (root / "receipts").mkdir()
    # Provider restarts must fail before downloading, initializing CUDA or
    # establishing a different capture epoch for this immutable session identity.
    claim = cloud.claim_producer(
        args.catalog_bucket, args.session_id, args.input_sha256
    )
    log(
        "cloud_producer_claimed",
        session_id=args.session_id,
        generation=claim["generation"],
    )
    proof = download(client, args, root / "source.zip", deadline)
    source = extract(root / "source.zip", root / "source")
    rig = Rig(source, source.parent)
    if (
        rig.config["source"]["url"] != args.source
        or rig.config["source"]["license"] != args.license
    ):
        raise ValueError("Source attribution differs from approved preset")
    download_seconds = time.monotonic() - started
    status_path, output = root / "source-status.json", root / "output"
    config = {
        "source_manifest": str(source),
        "source_root": str(source.parent),
        "output_dir": str(output),
        "cuda_extension": None,
        "cloud_warmup": True,
        "status_path": str(status_path),
        "session_id": args.session_id,
        "title": args.title,
        "source_seconds": args.source_seconds,
        "seed_available_after": args.seed_available_after,
        "bootstrap_steps": args.bootstrap_steps,
        "steps_per_update": args.steps_per_update,
        "decode_workers": args.decode_workers,
        "max_runtime_seconds": max(1, math.floor(deadline.remaining(1790) - 10)),
    }
    config_path = root / "worker.json"
    config_path.write_bytes(json_bytes(config))
    child = subprocess.Popen(
        [sys.executable, "-m", "rangle_live.cloud_child", str(config_path)],
        start_new_session=True,
    )
    receipts, evidence, failure = [], {}, None
    publications = LatestPublication(args, output / "index.json", cloud, deadline)
    terminal_publication = "not-needed"
    log(
        "cloud_worker_started",
        session_id=args.session_id,
        download=proof,
        download_and_extract_seconds=download_seconds,
    )
    try:
        while True:
            if deadline.remaining(1800) <= 10:
                raise TimeoutError(
                    "Cloud live work deadline reached; reserve cleanup time"
                )
            receipt = publications.poll()
            if receipt is not None:
                if receipt.get("public_http_confirmed_monotonic"):
                    receipts.append(receipt)
                    for item in receipt.get("confirmed_segments", []):
                        evidence.setdefault(item["sequence"], item)
                log(
                    "cloud_live_published",
                    session_id=args.session_id,
                    state=receipt["state"],
                    latest_sequence=receipt["latest_sequence"],
                    public_http_confirmed="public_http_confirmed_monotonic" in receipt,
                )
            if child.poll() is not None:
                if child.returncode:
                    raise RuntimeError(f"Training subprocess exited {child.returncode}")
                # A final write may race the preceding poll; publish its terminal index.
                final_receipt = publications.poll(wait=True)
                if final_receipt is not None:
                    receipts.append(final_receipt)
                    for item in final_receipt.get("confirmed_segments", []):
                        evidence.setdefault(item["sequence"], item)
                break
            time.sleep(min(0.25, deadline.remaining(0.25)))
    except BaseException as error:  # noqa: BLE001 - always reap the isolated child
        failure = error
    finally:
        # Cleanup cannot be interrupted halfway through leaving a live GPU child.
        previous = {
            number: signal.signal(number, signal.SIG_IGN)
            for number in (signal.SIGTERM, signal.SIGALRM)
        }
        try:
            termination = terminate_group(child)
            if failure:
                terminal_publication = "no-local-index"
            if failure and (output / "index.json").exists():
                try:
                    document = json.loads(read_json(output / "index.json"))
                    session = document["sessions"][0]
                    if session["state"] not in ("ended", "failed"):
                        session["state"] = "failed"
                    session["updated_at"] = protocol.now()
                    atomic_index(output / "index.json", json_bytes(document))
                    deadline.end = min(deadline.end, time.monotonic() + 5)

                    def terminal_timeout(_number, _frame):
                        raise TimeoutError("Terminal publication budget expired")

                    signal.signal(signal.SIGALRM, terminal_timeout)
                    signal.setitimer(signal.ITIMER_REAL, deadline.remaining(5))
                    publications.poll(wait=True)
                    terminal_publication = "confirmed"
                except (
                    PublishError,
                    OSError,
                    ValueError,
                    TimeoutError,
                    subprocess.SubprocessError,
                ):
                    terminal_publication = "failed"
                    log(
                        "cloud_terminal_publication_failed",
                        severity="WARN",
                        session_id=args.session_id,
                    )
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
            for number, handler in previous.items():
                signal.signal(number, handler)
    status = json.loads(status_path.read_bytes()) if status_path.exists() else {}
    capture_origin = status.get("capture_origin_monotonic")
    source_finished = status.get("source_finished_monotonic")
    confirmed = [row["public_http_confirmed_monotonic"] for row in evidence.values()]
    first = min(confirmed) if confirmed else None
    latencies = [row["capture_to_public_http_seconds"] for row in evidence.values()]
    summary = {
        "version": 1,
        "session_id": args.session_id,
        "input": proof,
        "outcome": "failed" if failure else "ended",
        "termination": termination,
        "terminal_publication": terminal_publication,
        "download_and_extract_seconds": download_seconds,
        "cloud_startup_to_capture_seconds": capture_origin - started
        if capture_origin
        else None,
        "first_public_http_since_capture_seconds": first - capture_origin
        if first and capture_origin
        else None,
        "first_public_http_before_source_completion": first < source_finished
        if first and source_finished
        else False,
        "source_completion_since_capture_seconds": source_finished - capture_origin
        if source_finished and capture_origin
        else None,
        "mean_capture_to_public_http_seconds": sum(latencies) / len(latencies)
        if latencies
        else None,
        "max_capture_to_public_http_seconds": max(latencies) if latencies else None,
        "published_snapshots": [evidence[key] for key in sorted(evidence)],
        "source_status": status,
        "latency_scope": "simulated capture PTS to verified public CDN live-index and model/scene bytes; excludes viewer polling/rendering and Vertex scheduling/image pull",
    }
    (root / "cloud-summary.json").write_bytes(json_bytes(summary))
    log("cloud_live_report", **summary)
    if failure:
        raise failure
    return summary


def main(argv=None):
    started = time.monotonic()
    args = parse_args(argv)
    deadline = Deadline(args.deadline)

    def stop(_number, _frame):
        raise KeyboardInterrupt

    previous = {
        number: signal.signal(number, stop)
        for number in (signal.SIGTERM, signal.SIGALRM)
    }
    signal.setitimer(signal.ITIMER_REAL, deadline.remaining(1800))
    client = cloud = None
    try:
        client, cloud = adc_clients(deadline)
        with tempfile.TemporaryDirectory(prefix="rangle-cloud-live-") as directory:
            supervise(args, client, cloud, deadline, directory, started)
    except KeyboardInterrupt:
        log("cloud_live_cancelled", session_id=args.session_id)
        return 130
    except Exception as error:  # noqa: BLE001 - one sanitized job boundary, no HTTP/token bodies
        log(
            "cloud_live_failed",
            severity="ERROR",
            session_id=args.session_id,
            error=str(error)[:1000],
        )
        return 1
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        for number, handler in previous.items():
            signal.signal(number, handler)
        if cloud is not None:
            cloud.session.close()
        if client is not None:
            client.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
