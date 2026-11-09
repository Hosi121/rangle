import gzip
import io
import json
import shutil
import struct
import subprocess
import tempfile
import unittest
import zipfile
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import numpy as np
from rangle_training.data import load_dataset, scene_camera
from rangle_training.packing import (
    TEMPORAL,
    compress_temporal,
    decode_temporal,
    pack_temporal,
    temporal_header,
    unpack_temporal,
)
from rangle_training.train import build_scene, parse_args
from test_contract import fixture


def temporal_fixture():
    values = fixture()
    values["images"] = np.repeat(values["images"][None], 3, axis=0)
    values["frame_times"] = np.array([0, 0.04, 0.08], dtype=np.float64)
    return values


def model_fixture():
    return (
        np.array([[1, 2, 3], [4, 5, 6]], dtype=np.float32),
        np.full((2, 3), 0.1, dtype=np.float32),
        np.array([[1, 0, 0, 0], [0.5, 0.5, 0.5, 0.5]], dtype=np.float32),
        np.array([[0.25, 0.5, 0.75], [1, 0, 0]], dtype=np.float32),
        np.array([0.8, 0], dtype=np.float32),
        np.array([[0.5, -1, 2], [1, 2, 3]], dtype=np.float32),
        np.array([0.04, 0.01], dtype=np.float32),
        np.array([0.08, 0.04], dtype=np.float32),
    )


class TemporalContractTests(unittest.TestCase):
    def load(self, values, mode="dynamic"):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "data.npz"
            np.savez_compressed(path, **values)
            return load_dataset(path, mode)

    def test_temporal_schema_and_camera_match_static(self):
        data = self.load(temporal_fixture())
        self.assertEqual(data.frame_rate, 25)
        self.assertEqual(data.images.shape, (3, 4, 64, 96, 3))
        self.assertEqual(
            scene_camera(data), scene_camera(self.load(fixture(), "static"))
        )

    def test_invalid_times_and_mode_are_rejected(self):
        for times in (
            [0, 0, 0],
            [0, -0.04, -0.08],
            [0.01, 0.05, 0.09],
            [0, 0.04, 0.09],
            [0, np.nan, 0.08],
            [0, 0.001, 0.002],
            np.array([0, 0.04, 0.08], dtype=np.float32),
        ):
            with self.subTest(times=times):
                values = temporal_fixture()
                values["frame_times"] = np.asarray(times)
                with self.assertRaises(ValueError):
                    self.load(values)
        for values, mode in (
            (fixture(), "dynamic"),
            (temporal_fixture(), "static"),
            (fixture(), "other"),
        ):
            with self.subTest(mode=mode), self.assertRaises(ValueError):
                self.load(values, mode)

    def test_temporal_frame_bounds_and_dtypes(self):
        for frames in (1, 33):
            values = temporal_fixture()
            values["images"] = np.repeat(values["images"][:1], frames, axis=0)
            values["frame_times"] = np.arange(frames, dtype=np.float64) / 25
            with self.subTest(frames=frames), self.assertRaises(ValueError):
                self.load(values)
        for key in ("images", "frame_times", "xyz"):
            values = temporal_fixture()
            values[key] = np.array([{"pickle": "forbidden"}], dtype=object)
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.load(values)

    def test_npy_forged_allocation_and_duplicate_members_rejected_before_load(self):
        values = temporal_fixture()
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "data.npz"
            np.savez_compressed(path, **values)
            with self.assertWarns(UserWarning), zipfile.ZipFile(path, "a") as archive:
                header = io.BytesIO()
                np.lib.format.write_array_header_1_0(
                    header,
                    {
                        "descr": "|u1",
                        "fortran_order": False,
                        "shape": (32, 64, 1080, 1920, 3),
                    },
                )
                archive.writestr("images.npy", header.getvalue())
            with patch("rangle_training.data.np.load") as unsafe_load:
                with self.assertRaises(ValueError):
                    load_dataset(path, "dynamic")
                unsafe_load.assert_not_called()
            # A unique forged member also fails the header/file-size check.
            with zipfile.ZipFile(path) as archive:
                members = {name: archive.read(name) for name in archive.namelist()}
            with zipfile.ZipFile(path, "w") as archive:
                for name, payload in members.items():
                    archive.writestr(name, payload)
            with patch("rangle_training.data.np.load") as unsafe_load:
                with self.assertRaises(ValueError):
                    load_dataset(path, "dynamic")
                unsafe_load.assert_not_called()

    def test_image_allocation_budgets_are_checked_before_numpy_load(self):
        for mode, shape in (
            ("static", (64, 1080, 1920, 3)),
            ("dynamic", (32, 64, 270, 480, 3)),
        ):
            values = temporal_fixture() if mode == "dynamic" else fixture()
            entries, contents = [], {}
            for key, array in values.items():
                data = io.BytesIO()
                if key == "images":
                    np.lib.format.write_array_header_1_0(
                        data,
                        {
                            "descr": "|u1",
                            "fortran_order": False,
                            "shape": shape,
                        },
                    )
                    size = len(data.getvalue()) + int(np.prod(shape))
                else:
                    np.save(data, array, allow_pickle=False)
                    size = len(data.getvalue())
                name = key + ".npy"
                entries.append(
                    SimpleNamespace(
                        filename=name,
                        file_size=size,
                        flag_bits=0,
                        compress_type=zipfile.ZIP_STORED,
                    )
                )
                contents[name] = data.getvalue()
            archive = MagicMock()
            archive.__enter__.return_value = archive
            archive.infolist.return_value = entries
            archive.open.side_effect = lambda entry, contents=contents: io.BytesIO(
                contents[entry.filename]
            )
            with (
                self.subTest(mode=mode),
                patch("rangle_training.data.zipfile.ZipFile", return_value=archive),
                patch("rangle_training.data.np.load") as unsafe_load,
            ):
                with self.assertRaisesRegex(ValueError, "image budget"):
                    load_dataset(Path("unused.npz"), mode)
                unsafe_load.assert_not_called()
            entries[0].file_size = (1 << 30) + 1
            with (
                patch("rangle_training.data.zipfile.ZipFile", return_value=archive),
                patch("rangle_training.data.np.load") as unsafe_load,
            ):
                with self.assertRaisesRegex(ValueError, "archive"):
                    load_dataset(Path("unused.npz"), mode)
                unsafe_load.assert_not_called()

    def test_cli_static_default_and_mode_specific_budget(self):
        required = [
            "--input-uri",
            "gs://bucket/input.npz",
            "--input-sha256",
            "a" * 64,
            "--output-prefix",
            "gs://bucket/generated/" + "b" * 32 + "/",
            "--job-id",
            "b" * 32,
            "--deadline",
            "2026-11-09T12:00:00Z",
            "--title",
            "VRU",
            "--source",
            "https://example.com",
            "--license",
            "CC",
        ]
        self.assertEqual(parse_args(required).mode, "static")
        self.assertEqual(parse_args(required).steps, 6000)
        self.assertEqual(
            parse_args(required + ["--mode", "dynamic", "--steps", "12000"]).steps,
            12000,
        )
        for mode, steps in (("static", 6001), ("dynamic", 12001), ("dynamic", 0)):
            with self.subTest(mode=mode, steps=steps), self.assertRaises(ValueError):
                parse_args(required + ["--mode", mode, "--steps", str(steps)])
        with self.assertRaises(SystemExit), patch("sys.stderr", new=io.StringIO()):
            parse_args(required + ["--mode", "unknown"])
        args = parse_args(required + ["--mode", "dynamic"])
        scene = build_scene(args, self.load(temporal_fixture()))
        self.assertEqual(scene["temporal"], {"url": "model.rgs"})
        self.assertEqual(scene["frameRate"], 25)
        self.assertEqual(scene["frameCount"], 3)
        self.assertNotIn("frames", scene)


class TemporalPackingTests(unittest.TestCase):
    def payload(self):
        return pack_temporal(*model_fixture(), frame_count=3, frame_rate=25)

    def test_layout_and_lossless_byte_roundtrip_preserve_all_ids(self):
        raw = self.payload()
        self.assertEqual(TEMPORAL.itemsize, 56)
        self.assertEqual(len(raw), 32 + 2 * 56)
        self.assertEqual(raw[:8], b"RANGL4D1")
        self.assertEqual(temporal_header(raw)[:3], (2, 3, 25))
        for key, offset in (
            ("position", 0),
            ("scale", 12),
            ("rgba", 24),
            ("quaternion", 28),
            ("velocity", 32),
            ("centre_time", 44),
            ("sigma", 48),
            ("peak", 52),
        ):
            self.assertEqual(TEMPORAL.fields[key][1], offset)
        packed = compress_temporal(raw)
        self.assertEqual(packed[:8], b"RANGL4Z1")
        self.assertEqual(decode_temporal(packed), raw)
        arrays = unpack_temporal(packed)
        for index in (0, 1, 4, 5, 6, 7):
            np.testing.assert_array_equal(arrays[index], model_fixture()[index])
        # The zero-alpha row remains as canonical primitive ID 1.
        self.assertEqual(len(arrays[0]), 2)
        np.testing.assert_allclose(arrays[3], model_fixture()[3], atol=1 / 255)

    def test_invalid_temporal_header_values_and_compressed_bombs_are_bounded(self):
        raw = self.payload()
        for offset, fmt, value in (
            (8, "I", 0),
            (8, "I", 500001),
            (12, "I", 55),
            (16, "I", 1),
            (20, "f", float("nan")),
            (24, "f", 1),
            (28, "I", 1),
        ):
            bad = bytearray(raw)
            struct.pack_into("<" + fmt, bad, offset, value)
            with (
                self.subTest(offset=offset, value=value),
                self.assertRaises(ValueError),
            ):
                unpack_temporal(bytes(bad))
        for column, value in (
            (0, float("nan")),
            (3, 0),
            (12, 0),
            (12, 1e-40),
            (13, 1.1),
        ):
            bad = bytearray(raw)
            struct.pack_into("<f", bad, 32 + column * 4, value)
            with self.subTest(column=column), self.assertRaises(ValueError):
                unpack_temporal(bytes(bad))
        for body in (b"x" * 113, b"x" * 111):
            bad = b"RANGL4Z1" + raw[8:32] + gzip.compress(body)
            with self.assertRaises(ValueError):
                decode_temporal(bad)

    @unittest.skipUnless(
        shutil.which("node"), "Node is needed for the existing JS codec oracle"
    )
    def test_existing_js_codec_decodes_python_bytes_exactly(self):
        codec = (
            Path(__file__).resolve().parents[2]
            / "apps/web/viewer/src/temporal-codec.js"
        )
        raw = self.payload()
        with tempfile.TemporaryDirectory() as directory:
            packed_path = Path(directory) / "model.rgs"
            packed_path.write_bytes(compress_temporal(raw))
            script = """import fs from 'node:fs';
                const {decodeTemporal, temporalHeader} = await import(process.argv[1]);
                const b = fs.readFileSync(process.argv[2]);
                const a = b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength);
                const raw = await decodeTemporal(a);
                const d = new DataView(raw); const t = 0.08;
                const peak = d.getFloat32(32+52,true), centre = d.getFloat32(32+44,true);
                const sigma = d.getFloat32(32+48,true), delta = t-centre;
                const position = d.getFloat32(32,true) + d.getFloat32(32+32,true)*delta;
                const opacity = peak*Math.exp(-0.5*(delta/sigma)**2);
                console.log(JSON.stringify({header:temporalHeader(raw),
                    hex:Buffer.from(raw).toString('hex'),position,opacity}));"""
            output = subprocess.check_output(
                [
                    "node",
                    "--input-type=module",
                    "-e",
                    script,
                    codec.as_uri(),
                    str(packed_path),
                ],
                text=True,
            )
            oracle = json.loads(output)
        self.assertEqual(bytes.fromhex(oracle["hex"]), raw)
        means, _, _, _, peak, velocity, centre, sigma = unpack_temporal(raw)
        delta = np.float32(0.08) - centre[0]
        self.assertAlmostEqual(
            oracle["position"], float(means[0, 0] + velocity[0, 0] * delta), places=6
        )
        expected = peak[0] * np.exp(-0.5 * (delta / sigma[0]) ** 2)
        self.assertAlmostEqual(oracle["opacity"], expected, places=6)


if __name__ == "__main__":
    unittest.main()
