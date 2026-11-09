"""Explicit local preprocessing profile; requires OpenCV and fails if absent."""

import json
import tempfile
import unittest
from pathlib import Path

import cv2
import numpy as np
from rangle_preprocessing import prepare_dataset
from rangle_training.data import load_dataset
from test_preprocessing import source_fixture


class MediaConversionTests(unittest.TestCase):
    def test_png_rgb_conversion_matches_npy_exactly(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, config, _ = source_fixture(directory)
            expected = np.empty((2, 4, 64, 96, 3), np.uint8)
            for view in range(4):
                names = []
                for frame in range(2):
                    pixels = np.empty((64, 96, 3), np.uint8)
                    pixels[:] = [40 + view * 5, 80 + frame * 10, 130]
                    expected[frame, view] = pixels
                    name = f"view-{view}-{frame}.png"
                    self.assertTrue(
                        cv2.imwrite(str(directory / name), pixels[:, :, ::-1])
                    )
                    names.append(name)
                config["views"][view]["media"] = {"images": names}
            manifest.write_text(json.dumps(config))
            output = directory / "png.npz"
            prepare_dataset(manifest, output)
            np.testing.assert_array_equal(
                load_dataset(output, "dynamic").images, expected
            )

    def test_lossless_video_uses_exact_selected_indices_and_rgb(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, config, _ = source_fixture(directory)
            config["frames"]["indices"] = [0, 2]
            expected = np.empty((2, 4, 64, 96, 3), np.uint8)
            for view in range(4):
                name = f"camera-{view}.avi"
                writer = cv2.VideoWriter(
                    str(directory / name), cv2.VideoWriter_fourcc(*"FFV1"), 25, (96, 64)
                )
                self.assertTrue(
                    writer.isOpened(),
                    "The explicit preprocessing profile requires FFV1 video support",
                )
                try:
                    for frame in range(3):
                        pixels = np.empty((64, 96, 3), np.uint8)
                        pixels[:] = [20 + view * 5, 40 + frame * 20, 150]
                        writer.write(pixels[:, :, ::-1])
                        if frame in (0, 2):
                            expected[frame // 2, view] = pixels
                finally:
                    writer.release()
                config["views"][view]["media"] = {"video": name}
            manifest.write_text(json.dumps(config))
            output = directory / "video.npz"
            prepare_dataset(manifest, output)
            data = load_dataset(output, "dynamic")
            np.testing.assert_array_equal(data.images, expected)
            np.testing.assert_array_equal(data.frame_times, [0, 0.08])
            self.assertEqual(data.frame_rate, 12.5)
            config["frames"]["fps"] = 30
            manifest.write_text(json.dumps(config))
            with self.assertRaisesRegex(ValueError, "fps"):
                prepare_dataset(manifest, directory / "wrong-fps.npz")
            self.assertFalse((directory / "wrong-fps.npz").exists())

    def test_explicit_rectification_resizes_intrinsics(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            manifest, config, _ = source_fixture(directory)
            config["output"]["width"] = 64
            manifest.write_text(json.dumps(config))
            output = directory / "resized.npz"
            prepare_dataset(manifest, output)
            data = load_dataset(output, "dynamic")
            self.assertEqual(data.images.shape, (2, 4, 64, 64, 3))
            np.testing.assert_allclose(data.Ks[:, 0, 0], 80 * 64 / 96)
            np.testing.assert_allclose(data.Ks[:, 0, 2], 32)


if __name__ == "__main__":
    unittest.main()
