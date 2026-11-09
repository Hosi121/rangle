"""Cloud boundary tests: bounded files, ADC transport, CAS and process cleanup."""

import hashlib
import io
import json
import os
import signal
import stat
import subprocess
import sys
import tempfile
import time
import unittest
import zipfile
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
from urllib.parse import parse_qs, unquote, urlsplit

from rangle_live import bundle, cloud
from rangle_live.output import json_bytes
from rangle_live.publishing import ADCStorage, PublishError, protocol
from test_preprocessing import source_fixture


def source_fixture_video(root):
    path, value, _ = source_fixture(root)
    for view in value["views"]:
        name = f"view-{view['id']}.mp4"
        (root / name).write_bytes(b"opaque encoded video fixture")
        view["media"] = {"video": name}
    path.write_text(json.dumps(value))
    return path


class BundleTests(unittest.TestCase):
    def test_roundtrip_checks_every_byte_and_never_overwrites(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            manifest = source_fixture_video(root)
            archive = root / "input.zip"
            result = bundle.pack(manifest, root, archive)
            self.assertEqual(
                result["input_sha256"], hashlib.sha256(archive.read_bytes()).hexdigest()
            )
            output = root / "decoded"
            bundle.extract(archive, output)
            self.assertEqual(
                (output / "view-0.mp4").read_bytes(), (root / "view-0.mp4").read_bytes()
            )
            self.assertEqual(
                json.loads((output / "source.json").read_bytes()),
                json.loads(manifest.read_bytes()),
            )
            before = archive.read_bytes()
            with self.assertRaises(FileExistsError):
                bundle.pack(manifest, root, archive)
            self.assertEqual(archive.read_bytes(), before)
            with self.assertRaises(FileExistsError):
                bundle.extract(archive, output)

    def test_traversal_symlinks_duplicates_unproved_bytes_and_hashes_rejected(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            manifest = source_fixture_video(root)
            original = root / "original.zip"
            bundle.pack(manifest, root, original)
            with zipfile.ZipFile(original) as valid:
                files = {
                    entry.filename: valid.read(entry) for entry in valid.infolist()
                }
            for invalid in (
                "../escape",
                "/absolute",
                ".",
                "a\\b",
                "a//b",
                "symlink",
                "extra",
                "hash",
                "duplicate",
            ):
                archive = root / "bad.zip"
                archive.unlink(missing_ok=True)
                with zipfile.ZipFile(archive, "w") as target:
                    for name, body in files.items():
                        if invalid == "hash" and name == "view-0.mp4":
                            body += b"corrupt"
                        target.writestr(name, body)
                    if invalid == "symlink":
                        entry = zipfile.ZipInfo("link")
                        entry.create_system = 3
                        entry.external_attr = (stat.S_IFLNK | 0o777) << 16
                        target.writestr(entry, "../outside")
                    elif invalid == "duplicate":
                        with patch("warnings.warn"):
                            target.writestr("source.json", files["source.json"])
                    elif invalid != "hash":
                        target.writestr(invalid, b"unproved")
                output = root / "decoded"
                with (
                    self.subTest(invalid=invalid),
                    self.assertRaises((ValueError, zipfile.BadZipFile)),
                ):
                    bundle.extract(archive, output)
                self.assertFalse(output.exists())
                self.assertEqual(list(root.glob(".source-*")), [])

    def test_expansion_and_proof_manifest_are_bounded_before_extraction(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "bad.zip"
            with zipfile.ZipFile(
                archive, "w", compression=zipfile.ZIP_DEFLATED
            ) as target:
                target.writestr("source.json", b"0" * 2048)
                target.writestr("bundle.json", b"{}")
            self.assertLess(archive.stat().st_size, 1000)
            with patch.object(bundle, "MAX_BYTES", 1000), self.assertRaises(ValueError):
                bundle.extract(archive, root / "out")
            self.assertFalse((root / "out").exists())


class Response:
    def __init__(self, status, body):
        self.status_code, self.body = status, body

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        return None

    def iter_content(self, _size):
        yield self.body


class Session:
    """Object-only storage HTTP emulator; never accepts bucket/list/patch calls."""

    def __init__(self):
        self.objects, self.calls = {}, []
        self.next_generation = 1
        self.conflicts = 0

    def request(self, method, url, data=None, headers=None, **options):
        self.assert_request(method, url, options)
        parsed, query = urlsplit(url), parse_qs(urlsplit(url).query)
        self.calls.append((method, url))
        if method == "GET":
            bucket, key = parsed.path.split("/b/", 1)[1].split("/o/", 1)
            saved = self.objects.get((unquote(bucket), unquote(key)))
            if saved is None:
                return Response(404, b"{}")
            metadata, body = saved
            if (
                "generation" in query
                and query["generation"][0] != metadata["generation"]
            ):
                return Response(404, b"{}")
            return Response(200, body if "alt" in query else json_bytes(metadata))
        bucket = unquote(parsed.path.split("/b/", 1)[1].split("/o", 1)[0])
        boundary = headers["Content-Type"].split("boundary=")[1].encode()
        parts = data.split(b"--" + boundary)[1:-1]
        metadata = json.loads(parts[0].split(b"\r\n\r\n", 1)[1].removesuffix(b"\r\n"))
        body = parts[1].split(b"\r\n\r\n", 1)[1].removesuffix(b"\r\n")
        key = (bucket, metadata["name"])
        expected = query["ifGenerationMatch"][0]
        previous = self.objects.get(key)
        actual = previous[0]["generation"] if previous else "0"
        if (
            expected != actual
            or self.conflicts
            and metadata["name"] == "live/index.json"
        ):
            if self.conflicts:
                self.conflicts -= 1
            return Response(412, b"{}")
        metadata.update(generation=str(self.next_generation), size=str(len(body)))
        self.next_generation += 1
        self.objects[key] = metadata, body
        return Response(200, json_bytes(metadata))

    @staticmethod
    def assert_request(method, url, options):
        assert method in ("GET", "POST") and url.startswith(
            "https://storage.googleapis.com/"
        )
        assert options["allow_redirects"] is False and options["stream"] is True
        assert 0 < options["timeout"] <= 90


class CloudTests(unittest.TestCase):
    def deadline(self):
        return cloud.Deadline(
            (datetime.now(timezone.utc) + timedelta(minutes=10)).isoformat()
        )

    def test_adc_storage_create_only_metadata_and_pointer_cas(self):
        session = Session()
        adapter = ADCStorage(session, self.deadline())
        body = b"original RGS bytes"
        adapter.create(
            "media",
            "live/ID/rev/model.rgs",
            body,
            "application/octet-stream",
            "immutable",
        )
        adapter.create(
            "media",
            "live/ID/rev/model.rgs",
            body,
            "application/octet-stream",
            "immutable",
        )
        with self.assertRaises(PublishError):
            adapter.create(
                "media",
                "live/ID/rev/model.rgs",
                b"changed",
                "application/octet-stream",
                "immutable",
            )
        initial = {"version": 1, "sessions": []}
        saved = adapter.commit("catalog", initial, "0")
        actual, generation = adapter.index("catalog")
        self.assertEqual((actual, generation), (initial, saved))
        with self.assertRaises(PublishError):
            adapter.commit("catalog", initial, "0")
        self.assertTrue(
            all(
                "ifGenerationMatch=" in url
                for method, url in session.calls
                if method == "POST"
            )
        )
        self.assertTrue(
            all("contentEncoding" not in meta for meta, _ in session.objects.values())
        )

    def test_real_protocol_media_barrier_preserves_other_sessions_and_cas_retries(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            output = root / "output"
            assets = output / "live-assets" / "ID_1" / "000001-0123456789abcdef"
            assets.mkdir(parents=True)
            (root / "receipts").mkdir()
            model = b"opaque; Go gate validation injected separately"
            (assets / "model.rgs").write_bytes(model)
            (assets / "scene.json").write_bytes(b"{}")
            metrics = {
                "mean_packed_psnr": 20,
                "mean_packed_ssim": 0.8,
                "mean_float_psnr": 20,
                "mean_float_ssim": 0.8,
                "stored_gaussians": 1,
                "training_seconds": 1,
                "encoding_seconds": 0.1,
                "evaluation_seconds": 0.1,
                "total_optimizer_steps": 300,
            }
            (assets / "metrics.json").write_bytes(json_bytes(metrics))
            segment = {
                "sequence": 1,
                "start_seconds": 0,
                "end_seconds": 0.04,
                "scene_url": "/"
                + (assets.relative_to(output) / "scene.json").as_posix(),
                "model_sha256": hashlib.sha256(model).hexdigest(),
                "model_bytes": len(model),
                "captured_at": "2026-11-09T00:00:01Z",
                "available_at": "2026-11-09T00:00:02Z",
                "training_seconds": 1,
                "mean_packed_psnr": 20,
                "mean_packed_ssim": 0.8,
            }
            local = {
                "id": "ID_1",
                "title": "title",
                "state": "live",
                "started_at": "2026-11-09T00:00:00Z",
                "updated_at": "2026-11-09T00:00:02Z",
                "target_latency_seconds": 30,
                "latest_input_time_seconds": 0.04,
                "dropped_windows": 0,
                "source": {
                    "label": "source",
                    "url": "https://source.example/data",
                    "license": "CC BY 4.0",
                    "caveat": "prior",
                },
                "segments": [segment],
            }
            path = output / "index.json"
            path.write_bytes(json_bytes({"version": 1, "sessions": [local]}))
            (root / "source-status.json").write_bytes(
                json_bytes({"capture_origin_monotonic": time.monotonic() - 2})
            )
            args = SimpleNamespace(
                media_origin="https://media.example",
                source=local["source"]["url"],
                media_bucket="media",
                catalog_bucket="catalog",
                session_id="ID_1",
            )
            adapter = ADCStorage(Session(), self.deadline())
            other = {"id": "other", "segments": []}
            adapter.commit("catalog", {"version": 1, "sessions": [other]}, "0")
            seen = []

            def remote(origin, key, expected, kind, deadline):
                self.assertEqual(adapter.session.objects["media", key][1], expected)
                seen.append(key)

            adapter.session.conflicts = 1
            with (
                patch.object(protocol, "verify") as gate,
                patch.object(
                    protocol.storage,
                    "access_token",
                    side_effect=AssertionError("no gcloud"),
                ),
                patch.object(protocol, "remote_file", side_effect=remote),
            ):
                receipt = cloud.publish(args, path, adapter, self.deadline())
            self.assertGreaterEqual(gate.call_count, 3)
            self.assertIn("live-index/ID_1.json", seen)
            self.assertEqual(receipt["latest_sequence"], 1)
            self.assertGreater(
                receipt["confirmed_segments"][0]["capture_to_public_http_seconds"], 0
            )
            index, _ = adapter.index("catalog")
            self.assertEqual(index["sessions"][0], other)
            self.assertTrue(
                all(
                    not key.endswith("metrics.json")
                    for _, key in adapter.session.objects
                )
            )
            self.assertEqual(
                index["sessions"][-1]["segments"][0]["model_sha256"],
                segment["model_sha256"],
            )

    def test_download_is_generation_pinned_bounded_and_checksummed(self):
        args = SimpleNamespace(
            input_bucket="input",
            input_key="datasets/live/source.zip",
            input_sha256=hashlib.sha256(b"zip").hexdigest(),
        )
        calls = []

        class Blob:
            size = 3
            generation = "12"
            content_encoding = None

            def reload(self, **options):
                calls.append(options)

            def download_to_file(self, target, **options):
                calls.append(options)
                target.write(b"zip")

        client = SimpleNamespace(
            bucket=lambda _bucket: SimpleNamespace(blob=lambda _key: Blob())
        )
        with tempfile.TemporaryDirectory() as directory:
            proof = cloud.download(
                client, args, Path(directory) / "source.zip", self.deadline()
            )
            self.assertEqual(proof["bytes"], 3)
            self.assertEqual(calls[-1]["if_generation_match"], 12)
            self.assertIsNone(calls[-1]["retry"])
            args.input_sha256 = "0" * 64
            with self.assertRaises(ValueError):
                cloud.download(
                    client, args, Path(directory) / "bad.zip", self.deadline()
                )
        with patch.object(cloud, "MAX_BYTES", 2), self.assertRaises(ValueError):
            cloud.LimitedDownload(io.BytesIO()).write(b"zip")

    def test_supervisor_reaps_a_process_with_nonterminating_threads(self):
        script = 'import signal,threading,time; signal.signal(signal.SIGTERM,signal.SIG_IGN); threading.Thread(target=threading.Event().wait).start(); print("ready",flush=True); time.sleep(60)'
        child = subprocess.Popen(
            [sys.executable, "-c", script],
            start_new_session=True,
            stdout=subprocess.PIPE,
            text=True,
        )
        try:
            self.assertEqual(child.stdout.readline().strip(), "ready")
            result = cloud.terminate_group(child, grace=0.05)
            self.assertTrue(result["forced_kill"])
            self.assertTrue(result["reaped"])
            self.assertEqual(child.returncode, -signal.SIGKILL)
        finally:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait()
            child.stdout.close()

    def test_supervisor_failure_reaps_child_emits_report_and_attempts_terminal_state(
        self,
    ):
        original_popen = subprocess.Popen
        children, logs = [], []
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            inputs, work = root / "inputs", root / "work"
            inputs.mkdir()
            work.mkdir()
            manifest = source_fixture_video(inputs)
            source = json.loads(manifest.read_bytes())["source"]
            args = SimpleNamespace(
                source=source["url"],
                license=source["license"],
                session_id="ID",
                title="title",
                source_seconds=10,
                seed_available_after=0.8,
                bootstrap_steps=300,
                steps_per_update=300,
                decode_workers=4,
            )

            def start(command, **options):
                config = json.loads(Path(command[-1]).read_bytes())
                output = Path(config["output_dir"])
                output.mkdir()
                (output / "index.json").write_bytes(
                    json_bytes({"version": 1, "sessions": [{"state": "starting"}]})
                )
                Path(config["status_path"]).write_bytes(
                    json_bytes(
                        {
                            "capture_origin_monotonic": time.monotonic(),
                            "source_finished_monotonic": None,
                        }
                    )
                )
                child = original_popen(
                    [
                        sys.executable,
                        "-c",
                        "import threading,time; threading.Thread(target=threading.Event().wait).start(); time.sleep(60)",
                    ],
                    start_new_session=True,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
                children.append(child)
                return child

            try:
                with (
                    patch.object(cloud, "download", return_value={"bytes": 1}),
                    patch.object(cloud, "extract", return_value=manifest),
                    patch.object(cloud.subprocess, "Popen", side_effect=start),
                    patch.object(
                        cloud, "publish", side_effect=PublishError("network failure")
                    ) as publish,
                    patch.object(
                        cloud,
                        "log",
                        side_effect=lambda event, **values: logs.append(
                            (event, values)
                        ),
                    ),
                    self.assertRaises(PublishError),
                ):
                    cloud.supervise(args, None, None, self.deadline(), work)
                self.assertIsNotNone(children[0].poll())
                self.assertEqual(publish.call_count, 2)
                report = json.loads((work / "cloud-summary.json").read_bytes())
                self.assertEqual(report["outcome"], "failed")
                self.assertTrue(report["termination"]["reaped"])
                self.assertFalse(report["first_public_http_before_source_completion"])
                self.assertEqual([e for e, _ in logs].count("cloud_live_report"), 1)
                self.assertEqual(
                    json.loads((work / "output" / "index.json").read_bytes())[
                        "sessions"
                    ][0]["state"],
                    "failed",
                )
            finally:
                for child in children:
                    if child.poll() is None:
                        os.killpg(child.pid, signal.SIGKILL)
                        child.wait()

    def test_frozen_cli_rejects_wrong_paths_scope_and_expired_deadline(self):
        deadline = (datetime.now(timezone.utc) + timedelta(minutes=5)).isoformat()
        base = [
            "--input-uri",
            "gs://input-bucket/datasets/live/source.zip",
            "--input-sha256",
            "a" * 64,
            "--output-prefix",
            "gs://media-bucket/live/1ID_/",
            "--catalog-bucket",
            "catalog-bucket",
            "--media-origin",
            "https://media.example",
            "--session-id",
            "1ID_",
            "--deadline",
            deadline,
            "--title",
            "title",
            "--source",
            "https://source.example/data",
            "--license",
            "CC BY 4.0",
        ]
        args = cloud.parse_args(base)
        self.assertEqual(
            (args.bootstrap_steps, args.steps_per_update, args.decode_workers),
            (300, 300, 4),
        )
        for flag, value in (
            ("--input-uri", "gs://input-bucket/datasets/other/source.zip"),
            ("--output-prefix", "gs://media-bucket/live/other/"),
            ("--media-origin", "http://media.example"),
            ("--source-seconds", "nan"),
        ):
            with (
                self.subTest(flag=flag),
                patch("sys.stderr", new_callable=io.StringIO),
                self.assertRaises(SystemExit),
            ):
                cloud.parse_args([*base, flag, value])
        with self.assertRaises(TimeoutError):
            cloud.Deadline("2020-01-01T00:00:00Z")


if __name__ == "__main__":
    unittest.main()
