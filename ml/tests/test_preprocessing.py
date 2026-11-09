import copy
import hashlib
import json
import struct
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import numpy as np
from rangle_preprocessing import prepare_dataset
from rangle_preprocessing.schema import read_manifest, validate_views
from rangle_preprocessing.sources import SourceFiles, load_seed
from rangle_preprocessing.vru import adapt_vru
from rangle_training.data import load_dataset
from test_contract import fixture


def source_fixture(directory, frames=2):
    source = fixture()
    np.savez(directory / "seeds.npz", xyz=source["xyz"], rgb=source["rgb"])
    views = []
    for view in range(4):
        names = []
        for frame in range(frames):
            name = f"view-{view}-frame-{frame}.npy"
            np.save(
                directory / name, np.full((64, 96, 3), view * 20 + frame * 5, np.uint8)
            )
            names.append(name)
        views.append(
            {
                "id": view,
                "split": "heldout" if view == 3 else "train",
                "media": {"images": names},
                "calibration": {
                    "width": 96,
                    "height": 64,
                    "K": source["Ks"][view].tolist(),
                    "world_to_camera": source["viewmats"][view].tolist(),
                },
            }
        )
    value = {
        "version": 1,
        "adapter": "calibrated-manifest",
        "source": {
            "name": "Synthetic fixture",
            "url": "https://example.com/dataset",
            "license": "CC0-1.0",
            "attribution": "Fixture author",
            "calibration_license": "CC0-1.0",
            "initialization_scope": "Synthetic seeds; no source-camera reconstruction",
        },
        "frames": {"indices": list(range(frames)), "fps": 25},
        "output": {"width": 96, "height": 64},
        "seed": {"path": "seeds.npz", "max_points": 100, "seed": 0},
        "normalization": "training-camera-centres",
        "views": views,
    }
    path = directory / "source.json"
    path.write_text(json.dumps(value))
    return path, value, source


class PreprocessingTests(unittest.TestCase):
    def test_generic_rgb_arrays_produce_safe_temporal_npz_and_provenance(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, _, source = source_fixture(directory)
            output = directory / "dataset.npz"
            metadata = prepare_dataset(manifest, output)
            loaded = load_dataset(output, "dynamic")
            self.assertEqual(loaded.images.shape, (2, 4, 64, 96, 3))
            np.testing.assert_array_equal(loaded.images[:, 2, 0, 0, 0], [40, 45])
            np.testing.assert_array_equal(loaded.frame_times, [0, 0.04])
            np.testing.assert_array_equal(loaded.train_indices, [0, 1, 2])
            np.testing.assert_array_equal(loaded.heldout_indices, [3])
            self.assertEqual(metadata["frame_rate"], 25)
            self.assertEqual(metadata["source"]["license"], "CC0-1.0")
            self.assertEqual(metadata["source"]["attribution"], "Fixture author")
            self.assertEqual(metadata["source_points"], 100)
            self.assertEqual(len(metadata["source_files"]), 9)
            self.assertNotIn(str(directory), output.with_suffix(".json").read_text())
            self.assertEqual(
                metadata["npz_sha256"], hashlib.sha256(output.read_bytes()).hexdigest()
            )
            # Normalization preserves all camera projections, including held-out views.
            for view in range(4):
                before = (
                    source["xyz"] @ source["viewmats"][view, :3, :3].T
                    + source["viewmats"][view, :3, 3]
                )
                after = (
                    loaded.xyz @ loaded.viewmats[view, :3, :3].T
                    + loaded.viewmats[view, :3, 3]
                )
                np.testing.assert_allclose(
                    before[:, :2] / before[:, 2:],
                    after[:, :2] / after[:, 2:],
                    atol=1e-6,
                )

    def test_static_schema_stride_and_overwrite_boundaries(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, value, _ = source_fixture(directory, frames=1)
            output = directory / "dataset.npz"
            prepare_dataset(manifest, output)
            self.assertEqual(load_dataset(output).images.ndim, 4)
            before = output.read_bytes()
            with self.assertRaises(FileExistsError):
                prepare_dataset(manifest, output)
            self.assertEqual(output.read_bytes(), before)
            output.unlink()
            with self.assertRaises(FileExistsError):
                prepare_dataset(manifest, output)
            output.with_suffix(".json").unlink()
            manifest, value, _ = source_fixture(directory, frames=3)
            value["frames"]["indices"] = [7, 9, 11]
            manifest.write_text(json.dumps(value))
            prepare_dataset(manifest, output)
            np.testing.assert_array_equal(
                load_dataset(output, "dynamic").frame_times, [0, 0.08, 0.16]
            )

    def test_invalid_manifest_calibration_paths_and_checksums_fail_without_output(self):
        changes = [
            "unknown",
            "nonuniform",
            "duplicate",
            "rotation",
            "overlap",
            "path",
            "checksum",
            "unused-checksum",
            "missing-license",
        ]
        for change in changes:
            with (
                self.subTest(change=change),
                tempfile.TemporaryDirectory() as temporary,
            ):
                directory = Path(temporary)
                manifest, value, _ = source_fixture(directory, frames=3)
                if change == "unknown":
                    value["unexpected"] = True
                elif change == "nonuniform":
                    value["frames"]["indices"] = [0, 1, 3]
                elif change == "duplicate":
                    value["views"][1]["id"] = 0
                elif change == "rotation":
                    value["views"][0]["calibration"]["world_to_camera"][0][0] = 2
                elif change == "overlap":
                    value["views"][3]["split"] = "train"
                elif change == "path":
                    value["views"][0]["media"]["images"][0] = "../escape.npy"
                elif change == "checksum":
                    value["checksums"] = {"seeds.npz": "0" * 64}
                elif change == "unused-checksum":
                    value["checksums"] = {"unused.npy": "0" * 64}
                else:
                    del value["source"]["license"]
                manifest.write_text(json.dumps(value))
                with self.assertRaises(ValueError):
                    prepare_dataset(manifest, directory / "output.npz")
                self.assertFalse((directory / "output.npz").exists())
                self.assertFalse((directory / "output.json").exists())

    def test_object_images_and_source_root_symlinks_are_rejected(self):
        with (
            tempfile.TemporaryDirectory() as temporary,
            tempfile.TemporaryDirectory() as outside,
        ):
            directory = Path(temporary)
            manifest, _, _ = source_fixture(directory)
            np.save(
                directory / "view-0-frame-0.npy",
                np.array([{"pickle": "forbidden"}], object),
            )
            with self.assertRaises(ValueError):
                prepare_dataset(manifest, directory / "output.npz")
            manifest, _, _ = source_fixture(directory)
            (directory / "view-0-frame-0.npy").unlink()
            np.save(Path(outside) / "outside.npy", np.zeros((64, 96, 3), np.uint8))
            (directory / "view-0-frame-0.npy").symlink_to(Path(outside) / "outside.npy")
            with self.assertRaises(ValueError):
                prepare_dataset(manifest, directory / "output.npz")

    def test_unsafe_seed_archive_is_rejected_without_output(self):
        for kind in ("object", "extra"):
            with self.subTest(kind=kind), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                manifest, _, source = source_fixture(directory)
                values = {"xyz": source["xyz"], "rgb": source["rgb"]}
                if kind == "object":
                    values["xyz"] = np.array([{"pickle": "forbidden"}], object)
                else:
                    values["unexpected"] = np.zeros(1)
                np.savez_compressed(directory / "seeds.npz", **values)
                with self.assertRaises(ValueError):
                    prepare_dataset(manifest, directory / "output.npz")
                self.assertFalse((directory / "output.npz").exists())

    def test_output_budget_fails_before_any_media_decode(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, value, _ = source_fixture(directory)
            value["frames"]["indices"] = list(range(32))
            value["output"] = {"width": 1920, "height": 1080}
            manifest.write_text(json.dumps(value))
            with patch("rangle_preprocessing.pipeline.decode_view") as decode:
                with self.assertRaisesRegex(ValueError, "image budget"):
                    prepare_dataset(manifest, directory / "output.npz")
                decode.assert_not_called()

    def test_binary_ply_matches_npz_and_seed_selection_is_deterministic(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            _, _, source = source_fixture(directory)
            dtype = np.dtype(
                [
                    ("x", "<f8"),
                    ("y", "<f8"),
                    ("z", "<f8"),
                    ("nx", "<f8"),
                    ("red", "u1"),
                    ("green", "u1"),
                    ("blue", "u1"),
                ]
            )
            vertices = np.zeros(120, dtype=dtype)
            xyz = np.resize(source["xyz"], (120, 3)).astype(np.float64)
            for i, key in enumerate(("x", "y", "z")):
                vertices[key] = xyz[:, i]
            for key in ("red", "green", "blue"):
                vertices[key] = 128
            path = directory / "cloud.ply"
            header = (
                "ply\nformat binary_little_endian 1.0\nelement vertex 120\n"
                + "".join(
                    f"property {'uchar' if key in ('red', 'green', 'blue') else 'double'} {key}\n"
                    for key in dtype.names
                )
                + "end_header\n"
            )
            path.write_bytes(header.encode() + vertices.tobytes())
            first = load_seed(path, 100, 0)
            second = load_seed(path, 100, 0)
            np.testing.assert_array_equal(first[0], second[0])
            np.testing.assert_array_equal(first[1], np.full((100, 3), 128, np.uint8))
            self.assertEqual(first[2], 120)

    def test_vru_colmap_adapter_resolves_view_ids_and_supplied_intrinsics(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, value, source = source_fixture(directory)
            del value["views"]
            value["adapter"] = "vru-colmap"
            value["vru"] = {
                "videos": "videos",
                "calibration": "calibration",
                "heldout_view_ids": [0],
            }
            sparse = directory / "calibration/frame000000/sparse/0"
            distorted = directory / "calibration/frame000000/distorted/sparse/0"
            sparse.mkdir(parents=True)
            distorted.mkdir(parents=True)
            (sparse / "cameras.txt").write_text(
                "\n".join(f"{i + 1} PINHOLE 96 64 80 80 48 32" for i in range(4))
            )
            (sparse / "images.txt").write_text(
                "\n".join(
                    f"{i + 1} 1 0 0 0 {-0.05 * i} 0 0 {i + 1} cam{i}.png\n"
                    for i in range(4)
                )
            )
            with (distorted / "cameras.bin").open("wb") as stream:
                stream.write(struct.pack("<Q", 4))
                for i in range(4):
                    stream.write(
                        struct.pack(
                            "<iiQQ8d", i + 1, 4, 96, 64, 80, 80, 48, 32, 0, 0, 0, 0
                        )
                    )
            manifest.write_text(json.dumps(value))
            config = read_manifest(manifest)
            views = validate_views(
                adapt_vru(config, SourceFiles(directory, {})), config
            )
            self.assertEqual([view["id"] for view in views], [0, 1, 2, 3])
            self.assertEqual(views[0]["media"]["video"], "videos/test/0.mp4")
            self.assertEqual(views[1]["media"]["video"], "videos/train/1.mp4")
            np.testing.assert_array_equal(
                views[1]["calibration"]["rectified"]["K"], source["Ks"][1]
            )
            invalid = copy.deepcopy(value)
            invalid["vru"]["heldout_view_ids"] = [99]
            with self.assertRaises(ValueError):
                adapt_vru(invalid, SourceFiles(directory, {}))


if __name__ == "__main__":
    unittest.main()
