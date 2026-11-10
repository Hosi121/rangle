#!/usr/bin/env python3
"""Storage transaction tests; real schemas/codecs are checked by the Go gate."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("live_publisher", Path(__file__).with_name("publish-live.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class Cloud:
    def __init__(self):
        self.document = {"version": 1, "sessions": []}
        self.generation = "1"
        self.objects = {}
        self.calls = []
        self.conflict = False
        self.public = None

    def read_document(self, bucket, key):
        return copy.deepcopy(self.public), "1"

    def index(self, _bucket):
        return copy.deepcopy(self.document), self.generation

    def create(self, bucket, key, body, *_metadata):
        self.calls.append(("create", key))
        if (bucket, key) in self.objects and self.objects[bucket, key] != body:
            raise publisher.PublishError("immutable object differs")
        self.objects[bucket, key] = body
        return {"bucket": bucket, "key": key, "generation": "10"}

    def commit(self, _bucket, document, expected, key="live/index.json", **_metadata):
        if key != "live/index.json":
            self.public = copy.deepcopy(document)
            self.calls.append(("commit-public", key))
            return "2"
        self.calls.append(("commit", "live/index.json"))
        if self.conflict or expected != self.generation:
            raise publisher.PublishError("concurrent index change")
        self.document = copy.deepcopy(document)
        self.generation = str(int(self.generation) + 1)
        return self.generation


class PublishLiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        base = Path(self.temp.name)
        root = base / "output"
        receipts = base / "receipts"
        root.mkdir(); receipts.mkdir()
        self.args = SimpleNamespace(root=str(root), receipts=str(receipts), gate="unused-test-gate",
                                    approved_source="https://source.example/data", origin="https://media.example",
                                    catalog_bucket="catalog", media_bucket="media")
        body = b"opaque bytes: codec validation belongs to the actual Go gate"
        digest = hashlib.sha256(body).hexdigest()
        directory = root / "live-assets" / "live-test" / ("000001-" + digest[:16])
        directory.mkdir(parents=True)
        (directory / "model.rgs").write_bytes(body)
        (directory / "scene.json").write_text("{}")
        segment = dict(sequence=1, start_seconds=0, end_seconds=.76,
                       scene_url="/" + str(directory.relative_to(root)) + "/scene.json",
                       model_sha256=digest, model_bytes=len(body), captured_at="2026-11-09T00:00:01Z",
                       available_at="2026-11-09T00:00:02Z", training_seconds=1,
                       mean_packed_psnr=20, mean_packed_ssim=.8)
        self.document = {"version": 1, "sessions": [dict(id="live-test", title="test", state="live",
                         started_at="2026-11-09T00:00:00Z", updated_at="2026-11-09T00:00:02Z",
                         source=dict(label="test", url=self.args.approved_source, license="CC BY 4.0", caveat="prior geometry"),
                         target_latency_seconds=30, latest_input_time_seconds=.76, dropped_windows=0, segments=[segment])]}
        self.cloud = Cloud()
        self.deadline = publisher.storage.Deadline(60)
        self.gate = patch.object(publisher, "verify")
        self.gate.start()
        self.addCleanup(self.gate.stop)

    def run_publish(self, remote=lambda *_args: None):
        return publisher.publish_once(self.args, self.document, self.cloud, self.deadline, remote=remote)

    def test_media_remote_barrier_archive_and_pointer_last_preserve_other_session(self):
        unrelated = dict(id="other", segments=[])
        self.cloud.document["sessions"].append(unrelated)
        fetched = []
        self.run_publish(remote=lambda _origin, key, *_args: fetched.append(key))
        self.assertEqual([key.rsplit("/", 1)[-1] for key in fetched], ["model.rgs", "scene.json"])
        self.assertEqual(self.cloud.calls[-1], ("commit", "live/index.json"))
        self.assertEqual(self.cloud.calls[-2], ("commit-public", "live-index/live-test.json"))
        self.assertTrue(self.cloud.calls[-3][1].startswith("live/snapshots/live-test/"))
        self.assertEqual(self.cloud.document["sessions"][0], unrelated)
        session = self.cloud.document["sessions"][1]
        self.assertTrue(session["segments"][0]["scene_url"].startswith("https://media.example/live/live-test/"))
        self.assertLessEqual(session["segments"][0]["available_at"], session["updated_at"])

    def test_remote_failure_keeps_active_index_and_can_resume_same_immutable_media(self):
        def fail(*_args):
            raise publisher.PublishError("injected remote failure")
        with self.assertRaises(publisher.PublishError):
            self.run_publish(remote=fail)
        self.assertEqual(self.cloud.document, {"version": 1, "sessions": []})
        self.assertFalse(any(call[0] == "commit" for call in self.cloud.calls))
        self.run_publish()
        self.assertEqual(len(self.cloud.document["sessions"]), 1)

    def test_conflict_cannot_overwrite_pointer_and_successful_rerun_is_idempotent(self):
        self.cloud.conflict = True
        with self.assertRaises(publisher.PublishError):
            self.run_publish()
        self.assertEqual(self.cloud.document["sessions"], [])
        self.cloud.conflict = False
        self.run_publish()
        calls = len(self.cloud.calls)
        result = self.run_publish()
        self.assertTrue(result["reused"])
        self.assertEqual(len(self.cloud.calls), calls)

    def test_changed_proof_source_or_progress_is_rejected_without_pointer_change(self):
        self.run_publish()
        original = copy.deepcopy(self.cloud.document)
        for field, value in (("model_sha256", "0" * 64), ("training_seconds", 99)):
            document = copy.deepcopy(self.document)
            document["sessions"][0]["segments"][0][field] = value
            with self.assertRaises(publisher.PublishError):
                publisher.publish_once(self.args, document, self.cloud, self.deadline)
            self.assertEqual(self.cloud.document, original)
        self.document["sessions"][0]["source"]["url"] = "https://unapproved.example/data"
        with self.assertRaises(publisher.PublishError): self.run_publish()
        self.assertEqual(self.cloud.document, original)

    def test_model_corruption_and_gate_failure_stop_before_writes(self):
        directory = next(Path(self.args.root).glob("live-assets/live-test/*"))
        (directory / "model.rgs").write_bytes(b"modified")
        with self.assertRaises(publisher.PublishError): self.run_publish()
        self.assertEqual(self.cloud.calls, [])
        with patch.object(publisher, "verify", side_effect=publisher.PublishError("bad schema")):
            with self.assertRaises(publisher.PublishError): self.run_publish()
        self.assertEqual(self.cloud.calls, [])


if __name__ == "__main__":
    unittest.main()
