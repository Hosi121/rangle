import gzip
import tempfile
import unittest
from pathlib import Path

import numpy as np
from rangle_training.data import load_dataset, scene_camera
from rangle_training.packing import SPLAT, pack_splats, unpack_splats
from rangle_training.train import uri


def fixture():
    cameras = np.repeat(np.eye(4, dtype=np.float32)[None], 4, axis=0)
    cameras[:, 0, 3] = np.arange(4) * -0.05
    points = np.random.default_rng(0).uniform(-0.1, 0.1, (100, 3)).astype(np.float32)
    points[:, 2] += 2
    return {
        "images": np.zeros((4, 64, 96, 3), np.uint8),
        "viewmats": cameras,
        "Ks": np.repeat(
            np.array([[[80.0, 0, 48], [0, 80, 32], [0, 0, 1]]], np.float32), 4, axis=0
        ),
        "xyz": points,
        "rgb": np.full((100, 3), 128, np.uint8),
        "train_indices": np.array([0, 1, 2]),
        "heldout_indices": np.array([3]),
    }


class ContractTests(unittest.TestCase):
    def load(self, values):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "input.npz"
            np.savez_compressed(path, **values)
            return load_dataset(path)

    def test_safe_calibrated_dataset_and_opencv_camera(self):
        data = self.load(fixture())
        camera = scene_camera(data)
        np.testing.assert_allclose(camera["position"], [0, 0, 0])
        np.testing.assert_allclose(camera["up"], [0, -1, 0])
        direction = np.array(camera["lookAt"]) - camera["position"]
        np.testing.assert_allclose(direction / np.linalg.norm(direction), [0, 0, 1])
        self.assertEqual(camera["referenceImageHeight"], 64)
        self.assertEqual(
            camera["intrinsics"],
            {"width": 96, "height": 64, "fx": 80.0, "fy": 80.0, "cx": 48.0, "cy": 32.0},
        )

    def test_corrupt_or_leaking_evaluation_contract_is_rejected(self):
        for change in (
            "overlap",
            "rotation",
            "nan",
            "object",
            "extra",
            "negative_index",
        ):
            with self.subTest(change=change):
                values = fixture()
                if change == "overlap":
                    values["heldout_indices"] = np.array([0])
                elif change == "rotation":
                    values["viewmats"][0, 0, 0] = 2
                elif change == "nan":
                    values["xyz"][0, 0] = np.nan
                elif change == "object":
                    values["images"] = np.array([{"pickle": "forbidden"}], dtype=object)
                elif change == "extra":
                    values["unexpected"] = np.ones(1)
                else:
                    values["heldout_indices"] = np.array([-1])
                with self.assertRaises(ValueError):
                    self.load(values)

    def test_splat_layout_and_gzip_transport(self):
        means = np.array([[1.0, 2, 3], [4, 5, 6]], np.float32)
        scales = np.full((2, 3), 0.1, np.float32)
        quats = np.array([[1.0, 0, 0, 0], [1, 0, 0, 0]], np.float32)
        colors = np.array([[0.25, 0.5, 0.75], [0, 0, 0]], np.float32)
        opacity = np.array([0.9, 0], np.float32)
        payload = pack_splats(means, scales, quats, colors, opacity)
        self.assertEqual(SPLAT.itemsize, 32)
        self.assertEqual(len(payload), 32)
        self.assertEqual(gzip.decompress(gzip.compress(payload, mtime=0)), payload)
        restored = unpack_splats(payload)
        np.testing.assert_array_equal(restored[0], means[:1])
        np.testing.assert_array_equal(restored[1], scales[:1])
        np.testing.assert_allclose(restored[2], quats[:1], atol=1e-5)
        np.testing.assert_allclose(restored[3], colors[:1], atol=1 / 255)
        np.testing.assert_allclose(restored[4], opacity[:1], atol=1 / 255)

    def test_splat_rejects_nonfinite_or_invalid_model(self):
        arrays = (
            np.zeros((1, 3), np.float32),
            np.ones((1, 3), np.float32),
            np.array([[1, 0, 0, 0]], np.float32),
            np.zeros((1, 3), np.float32),
            np.ones(1, np.float32),
        )
        for index in (0, 1, 2):
            with self.subTest(index=index):
                bad = [array.copy() for array in arrays]
                bad[index][0] = np.nan
                with self.assertRaises(ValueError):
                    pack_splats(*bad)

    def test_storage_uris_are_explicit_and_contain_no_credentials(self):
        self.assertEqual(
            uri("gs://example/generated/abc/"), ("example", "generated/abc/")
        )
        for value in (
            "https://example/model",
            "gs://user:secret@example/model",
            "gs://example/../model",
            "gs://example/model?token=secret",
            "gs://example/",
        ):
            with self.subTest(value=value), self.assertRaises(ValueError):
                uri(value)


if __name__ == "__main__":
    unittest.main()
