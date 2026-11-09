"""Contract tests run in the lightweight CI environment (no Torch/CUDA/OpenCV)."""

import copy
import hashlib
import io
import json
import tempfile
import threading
import time
import unittest
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import patch

import numpy as np
from rangle_live.__main__ import main, parse_args
from rangle_live.output import Publisher
from rangle_live.source import Rig, produce
from rangle_live.window import Frame, LatestWindow, Window
from rangle_training.packing import pack_temporal
from test_preprocessing import source_fixture


def frame(number):
    pts = number / 25
    return Frame(number, pts, 100 + pts, 100 + pts, np.zeros((1, 1, 1, 3), np.uint8))


def wire(frames=2):
    return pack_temporal(
        np.zeros((1, 3)),
        np.ones((1, 3)) * 0.01,
        np.array([[1, 0, 0, 0]]),
        np.ones((1, 3)) * 0.5,
        np.array([0.5]),
        np.zeros((1, 3)),
        np.array([0.02]),
        np.array([0.1]),
        frame_count=frames,
        frame_rate=25,
    )


class LiveWindowTests(unittest.TestCase):
    def test_pending_window_is_bounded_and_latest_wins(self):
        bus = LatestWindow()
        bus.observe(frame(10))
        for number in range(1, 11):
            bus.offer(Window((frame(number - 1), frame(number)), number))
            self.assertEqual(bus.status()["pending_windows"], 1)
        self.assertEqual(bus.status()["dropped_windows"], 9)
        self.assertEqual(bus.take().end, 0.4)
        self.assertEqual(bus.status()["pending_windows"], 0)
        with self.assertRaises(ValueError):
            bus.offer(Window((frame(11), frame(12)), 11))
        with self.assertRaises(ValueError):
            bus.observe(frame(9))
        bus.finish(101)
        self.assertIsNone(bus.take())
        with self.assertRaises(RuntimeError):
            bus.offer(Window((frame(9), frame(10)), 12))

    def test_input_deadline_and_source_cancellation_finish_cleanly(self):
        bus = LatestWindow()
        with self.assertRaises(TimeoutError):
            bus.take(time.monotonic())
        stop = threading.Event()
        stop.set()
        decoder = type("Decoder", (), {"close": lambda self: None})()
        produce(
            decoder,
            bus,
            stop,
            100,
            50,
            25,
            20,
            0.8,
            lambda *_args, **_kwargs: None,
            clock=lambda: 100,
        )
        self.assertIsNone(bus.take())
        self.assertEqual(bus.status()["frames_received"], 0)

    def test_original_pts_is_paced_and_first_window_precedes_completion(self):
        now = [100.0]
        offered = []
        bus = LatestWindow()
        original_offer = bus.offer

        def offer(window):
            offered.append((window, bus.status()["source_finished"]))
            original_offer(window)

        bus.offer = offer

        class Decoder:
            closed = False

            def read(self, number):
                self_test.assertGreaterEqual(now[0] + 1e-8, 100 + number / 25)
                return number / 25, frame(number).images

            def close(self):
                self.closed = True

        def wait(delay):
            now[0] += delay
            return False

        self_test, decoder = self, Decoder()
        produce(
            decoder,
            bus,
            threading.Event(),
            100,
            50,
            25,
            20,
            0.8,
            lambda *_args, **_kwargs: None,
            clock=lambda: now[0],
            wait=wait,
        )
        first = offered[0][0]
        self.assertEqual([f.number for f in first.frames], list(range(1, 21)))
        self.assertEqual(first.end, 0.8)
        self.assertTrue(all(not done for _, done in offered))
        self.assertEqual(bus.status()["frames_received"], 50)
        self.assertEqual(bus.take().frames[-1].number, 49)
        self.assertEqual(bus.status()["dropped_windows"], len(offered) - 1)
        self.assertTrue(decoder.closed)

    def test_decoder_error_reaches_consumer_and_closes(self):
        class Decoder:
            closed = False

            def read(self, number):
                raise ValueError("source failure")

            def close(self):
                self.closed = True

        bus, decoder = LatestWindow(), Decoder()
        produce(
            decoder,
            bus,
            threading.Event(),
            100,
            50,
            25,
            20,
            0.8,
            lambda *_args, **_kwargs: None,
            clock=lambda: 100,
        )
        with self.assertRaisesRegex(RuntimeError, "producer failed") as raised:
            bus.take()
        self.assertIsInstance(raised.exception.__cause__, ValueError)
        self.assertTrue(decoder.closed)


class LivePublicationTests(unittest.TestCase):
    def publisher(self, root):
        return Publisher(
            root,
            "basketball-live",
            "Test live",
            {
                "label": "source",
                "url": "https://example.test/source",
                "license": "CC BY 4.0",
                "caveat": "author seed prior",
            },
            100,
            datetime(2026, 11, 9, tzinfo=timezone.utc),
            clock=lambda: 102,
        )

    def publish(self, publisher, number=0):
        window = Window((frame(number), frame(number + 1)), number + 1)
        return publisher.publish(
            wire(),
            {
                "training_seconds": 0.1,
                "mean_packed_psnr": 20.0,
                "mean_packed_ssim": 0.8,
            },
            window,
            {"position": [0, 0, 1]},
            {"latest_input_time_seconds": window.end, "dropped_windows": 0},
        )

    def test_assets_then_scene_then_atomic_index_and_exact_contract(self):
        from rangle_live import output

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "out"
            publisher = self.publisher(root)
            order = []
            write, index = output.immutable_file, output.atomic_index

            def record_write(path, data):
                order.append(path.name)
                write(path, data)

            def record_index(path, data):
                order.append(path.name)
                index(path, data)

            with (
                patch.object(output, "immutable_file", record_write),
                patch.object(output, "atomic_index", record_index),
            ):
                result = self.publish(publisher)
            self.assertEqual(
                order, ["model.rgs", "metrics.json", "scene.json", "index.json"]
            )
            session = json.loads((root / "index.json").read_text())["sessions"][0]
            self.assertEqual(
                set(session),
                {
                    "id",
                    "title",
                    "state",
                    "started_at",
                    "updated_at",
                    "target_latency_seconds",
                    "latest_input_time_seconds",
                    "dropped_windows",
                    "source",
                    "segments",
                },
            )
            segment = session["segments"][0]
            self.assertEqual(
                set(segment),
                {
                    "sequence",
                    "start_seconds",
                    "end_seconds",
                    "scene_url",
                    "model_sha256",
                    "model_bytes",
                    "captured_at",
                    "available_at",
                    "training_seconds",
                    "mean_packed_psnr",
                    "mean_packed_ssim",
                },
            )
            digest = hashlib.sha256(wire()).hexdigest()
            self.assertEqual(segment["model_sha256"], digest)
            self.assertEqual(
                segment["scene_url"],
                f"/live-assets/basketball-live/000001-{digest[:16]}/scene.json",
            )
            scene_path = root / segment["scene_url"].lstrip("/")
            scene = json.loads(scene_path.read_text())
            self.assertEqual(scene["temporal"], {"url": "model.rgs"})
            self.assertEqual(
                scene["attribution"],
                {
                    "label": session["title"],
                    "source": session["source"]["url"],
                    "license": session["source"]["license"],
                },
            )
            self.assertEqual((scene_path.parent / "model.rgs").read_bytes(), wire())
            self.assertEqual(result["capture_to_local_snapshot_seconds"], 102 - 100.04)
            with self.assertRaises(FileExistsError):
                self.publisher(root)

    def test_index_retains_latest_32_and_fixed_camera(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "out"
            publisher = self.publisher(root)
            for number in range(33):
                self.publish(publisher, number)
            data = (root / "index.json").read_bytes()
            session = json.loads(data)["sessions"][0]
            self.assertEqual(
                [s["sequence"] for s in session["segments"]], list(range(2, 34))
            )
            self.assertLess(len(data), 524288)
            for segment in session["segments"]:
                scene = json.loads(
                    (root / segment["scene_url"].lstrip("/")).read_text()
                )
                self.assertEqual(scene["camera"], {"position": [0, 0, 1]})
            publisher.finish(
                "ended", {"latest_input_time_seconds": 1.32, "dropped_windows": 2}
            )
            self.assertEqual(
                json.loads((root / "index.json").read_text())["sessions"][0]["state"],
                "ended",
            )

    def test_camera_convention_cannot_change_between_revisions(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "out"
            publisher = self.publisher(root)
            self.publish(publisher)
            before = (root / "index.json").read_bytes()
            with self.assertRaisesRegex(ValueError, "camera/world"):
                publisher.publish(
                    wire(),
                    {},
                    Window((frame(1), frame(2)), 2),
                    {"position": [0, 0, 2]},
                    {"latest_input_time_seconds": 0.08},
                )
            self.assertEqual((root / "index.json").read_bytes(), before)

    def test_failed_scene_never_references_partial_assets(self):
        from rangle_live import output

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "out"
            publisher = self.publisher(root)
            before = (root / "index.json").read_bytes()
            original = output.immutable_file

            def fail_scene(path, data):
                if path.name == "scene.json":
                    raise OSError("disk fault")
                original(path, data)

            with (
                patch.object(output, "immutable_file", fail_scene),
                self.assertRaises(OSError),
            ):
                self.publish(publisher)
            self.assertEqual((root / "index.json").read_bytes(), before)
            self.assertEqual(list(publisher.session_dir.iterdir()), [])

    def test_timeline_future_and_invalid_identity_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            publisher = self.publisher(Path(directory) / "out")
            for frames, latest in ((3, 1), (2, 0)):
                with self.assertRaises(ValueError):
                    publisher.publish(
                        wire(frames),
                        {},
                        Window((frame(0), frame(1)), 1),
                        {},
                        {"latest_input_time_seconds": latest},
                    )
            with self.assertRaises(ValueError):
                Publisher(
                    Path(directory) / "bad",
                    "../escape",
                    "title",
                    {},
                    100,
                    datetime.now(timezone.utc),
                )


class LiveRigTests(unittest.TestCase):
    def manifest(self, root):
        path, value, _ = source_fixture(root)
        value["frames"]["fps"] = 30
        for view in value["views"]:
            name = f"view-{view['id']}.mp4"
            (root / name).touch()
            view["media"] = {"video": name}
        path.write_text(json.dumps(value))
        return path, value

    def test_generic_rig_derives_dimensions_fps_split_and_normalizes_once(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path, _ = self.manifest(root)
            rig = Rig(path, root)
            self.assertEqual(
                (rig.width, rig.height, len(rig.views), rig.window_frames, rig.fps),
                (96, 64, 4, 2, 30),
            )
            np.testing.assert_array_equal(rig.train, [0, 1, 2])
            np.testing.assert_array_equal(rig.heldout, [3])
            before = rig.matrices.copy()
            with self.assertRaises(ValueError):
                rig.admit_seed(0, 0.04)
            self.assertIsNone(rig.xyz)
            rig.admit_seed(0.04, 0.04)
            self.assertEqual(rig.xyz.shape, (100, 3))
            np.testing.assert_array_equal(rig.matrices, before)
            self.assertFalse(rig.provenance["trained_model_warm_start"])

    def test_offline_frame_selection_does_not_define_live_start_or_cadence(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path, value = self.manifest(root)
            value["frames"]["indices"] = [7, 9]
            path.write_text(json.dumps(value))
            rig = Rig(path, root)
            self.assertEqual((rig.window_frames, rig.fps), (2, 30))
            # produce() always starts from original recorded frame zero; the
            # separate pacing test verifies the actual decoder call sequence.

    def test_shared_bounds_and_live_video_template_are_strict(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path, original = self.manifest(root)
            for invalid in (
                "frames",
                "views",
                "images",
                "budget",
                "checksum",
            ):
                value = copy.deepcopy(original)
                if invalid == "frames":
                    value["frames"]["indices"] = [0]
                elif invalid == "views":
                    value["views"] = value["views"][:3]
                elif invalid == "images":
                    value["views"][0]["media"] = {"images": ["a", "b"]}
                elif invalid == "budget":
                    value["frames"]["indices"] = list(range(32))
                    value["output"] = {"width": 1920, "height": 1080}
                elif invalid == "checksum":
                    value["checksums"] = {"view-0.mp4": "0" * 64}
                path.write_text(json.dumps(value))
                with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                    Rig(path, root)


class LiveArgumentTests(unittest.TestCase):
    def test_help_exits_before_importing_runtime_dependencies(self):
        with (
            patch.dict("sys.modules", {"rangle_live.runner": None}),
            patch("sys.stdout", new_callable=io.StringIO),
            self.assertRaises(SystemExit) as raised,
        ):
            main(["--help"])
        self.assertEqual(raised.exception.code, 0)

    def test_runtime_failure_has_one_structured_error(self):
        base = [
            "--source-manifest",
            "source.json",
            "--source-root",
            "data",
            "--output-dir",
            "out",
            "--seed-available-after",
            "0",
        ]
        with (
            patch("rangle_live.runner.run", side_effect=ValueError("invalid source")),
            patch("sys.stdout", new_callable=io.StringIO) as stream,
            self.assertRaises(SystemExit) as raised,
        ):
            main(base)
        self.assertEqual(raised.exception.code, 1)
        events = [json.loads(line) for line in stream.getvalue().splitlines()]
        self.assertEqual(len(events), 1)
        self.assertEqual(events[0]["severity"], "ERROR")
        self.assertEqual(events[0]["service"], "rangle-live")
        self.assertEqual(events[0]["error"], "invalid source")

    def test_finite_cli_budgets(self):
        base = [
            "--source-manifest",
            "source.json",
            "--source-root",
            "data",
            "--output-dir",
            "out",
            "--seed-available-after",
            "0.8",
        ]
        args = parse_args(base)
        self.assertEqual((args.bootstrap_steps, args.steps_per_update), (300, 300))
        for flag, value in (
            ("--steps-per-update", "2001"),
            ("--source-seconds", "1801"),
            ("--seed-available-after", "-0.01"),
            ("--max-runtime-seconds", "1801"),
            ("--decode-workers", "33"),
        ):
            with self.subTest(flag=flag), self.assertRaises(SystemExit):
                parse_args([*base, flag, value])


if __name__ == "__main__":
    unittest.main()
