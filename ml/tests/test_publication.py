"""Publication boundaries are verified without CUDA or external storage calls."""

import gzip
import hashlib
import io
import json
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import numpy as np
from rangle_training.packing import compress_temporal, pack_temporal
from rangle_training.train import main
from test_contract import fixture
from test_temporal import model_fixture, temporal_fixture


class PublicationTests(unittest.TestCase):
    def run_worker(self, mode, fail_name=None):
        values = temporal_fixture() if mode == "dynamic" else fixture()
        source = io.BytesIO()
        np.savez_compressed(source, **values)
        dataset = source.getvalue()
        prefix = "generated/" + "a" * 32 + "/"
        published, calls = {}, []
        events = []

        class Blob:
            content_encoding = None
            size = len(dataset)
            generation = 7

            def __init__(self, name):
                self.name = name

            def reload(self, **kwargs):
                self.reload_options = kwargs

            def download_to_filename(self, path, **kwargs):
                self.download_options = kwargs
                Path(path).write_bytes(dataset)
                self.download = kwargs

            def upload_from_filename(self, path, **kwargs):
                calls.append((self.name, kwargs))
                if Path(path).name == fail_name:
                    raise RuntimeError("Storage publication failed")
                published[self.name] = (
                    Path(path).read_bytes(),
                    self.content_encoding,
                    kwargs["content_type"],
                )

        input_blob = Blob("input.npz")

        class Bucket:
            def blob(self, name):
                return input_blob if name == "input.npz" else Blob(name)

        class Client:
            closed = False

            def bucket(self, name):
                return Bucket()

            def close(self):
                self.closed = True

        client = Client()
        args = SimpleNamespace(
            mode=mode,
            steps=2,
            job_id="a" * 32,
            input_sha256=hashlib.sha256(dataset).hexdigest(),
            input_uri="gs://bucket/input.npz",
            output_prefix="gs://bucket/" + prefix,
            deadline=(datetime.now(timezone.utc) + timedelta(minutes=10)).isoformat(),
            title="VRU",
            source="https://example.com",
            license="CC",
        )
        dynamic_model = compress_temporal(
            pack_temporal(*model_fixture(), frame_count=3, frame_rate=25)
        )
        payload = dynamic_model if mode == "dynamic" else b"static model"

        def optimize(data, steps, deadline, directory):
            (directory / "heldout-3-frame-0.webp").write_bytes(b"diagnostic")
            return payload, {"training_seconds": 1, "stored_gaussians": 2}

        with (
            patch("rangle_training.train.parse_args", return_value=args),
            patch("rangle_training.train.optimize", side_effect=optimize),
            patch("rangle_training.temporal.optimize_dynamic", side_effect=optimize),
            patch("rangle_training.train.threading.Timer"),
            patch(
                "rangle_training.train.log",
                side_effect=lambda event, **kw: events.append(event),
            ),
        ):
            if fail_name:
                with self.assertRaisesRegex(RuntimeError, "Storage publication failed"):
                    main(storage_client_factory=lambda: client)
            else:
                main(storage_client_factory=lambda: client)
        self.assertTrue(client.closed)
        self.assertEqual(input_blob.download["if_generation_match"], 7)
        for _, kwargs in calls:
            self.assertEqual(kwargs["if_generation_match"], 0)
        return published, calls, events, prefix, payload

    def test_dynamic_internal_gzip_has_no_http_encoding_and_scene_is_last(self):
        published, calls, events, prefix, payload = self.run_worker("dynamic")
        model, encoding, content_type = published[prefix + "model.rgs"]
        self.assertEqual(model, payload)
        self.assertIsNone(encoding)
        self.assertEqual(content_type, "application/octet-stream")
        self.assertEqual(calls[-1][0], prefix + "scene.json")
        self.assertEqual(events[-1], "training_completed")
        scene = json.loads(published[prefix + "scene.json"][0])
        self.assertEqual(scene["temporal"], {"url": "model.rgs"})
        self.assertEqual(scene["frameCount"], 3)
        metrics = json.loads(published[prefix + "metrics.json"][0])
        self.assertEqual(metrics["mode"], "dynamic")
        self.assertEqual(metrics["model_sha256"], hashlib.sha256(payload).hexdigest())

    def test_static_publication_keeps_existing_splat_transport_and_scene(self):
        published, calls, _, prefix, payload = self.run_worker("static")
        model, encoding, _ = published[prefix + "model.splat"]
        self.assertEqual(gzip.decompress(model), payload)
        self.assertEqual(encoding, "gzip")
        scene = json.loads(published[prefix + "scene.json"][0])
        self.assertEqual(scene["frames"], ["model.splat"])
        self.assertEqual(scene["frameRate"], 1)
        self.assertNotIn("temporal", scene)
        self.assertEqual(calls[-1][0], prefix + "scene.json")
        metrics = json.loads(published[prefix + "metrics.json"][0])
        self.assertEqual(metrics["splat_bytes"], len(payload))

    def test_partial_upload_never_publishes_scene_or_success(self):
        published, _, events, prefix, _ = self.run_worker("dynamic", "metrics.json")
        self.assertNotIn(prefix + "scene.json", published)
        self.assertNotIn("training_completed", events)


if __name__ == "__main__":
    unittest.main()
