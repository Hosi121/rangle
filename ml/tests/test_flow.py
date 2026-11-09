"""Unsafe/off-profile inputs fail before optional OpenCV/Torch imports."""

import unittest

import numpy as np
from rangle_training.flow import prepare_observed_flow


class ObservedFlowInputTests(unittest.TestCase):
    def test_rejects_future_gaps_heldout_and_wrong_identity_before_ml_imports(self):
        base = {
            "images": np.zeros((2, 12, 16, 16, 3), np.uint8),
            "frame_times_seconds": [0.0, 0.04],
            "frame_numbers": [0, 1],
            "view_ids": list(range(12)),
            "train_indices": [i for i in range(12) if i not in (0, 10)],
            "heldout_indices": [0, 10],
            "latest_arrived_time_seconds": 0.04,
        }
        for changes in (
            {"latest_arrived_time_seconds": 0.0},
            {"frame_times_seconds": [0.0, 0.08]},
            {"frame_numbers": [0, 2]},
            {"frame_numbers": [1, 2]},
            {"frame_times_seconds": [0.0, float("nan")]},
            {"view_ids": [0] * 12},
            {"view_ids": [100 + i for i in range(12)]},
            {"train_indices": list(range(12))},
            {
                "heldout_indices": [0, 1, 10],
                "train_indices": [i for i in range(12) if i not in (0, 1, 10)],
            },
            {"images": base["images"].astype(np.float32)},
            {"images": [None, None]},
        ):
            with self.subTest(changes=list(changes)), self.assertRaises(ValueError):
                prepare_observed_flow(**{**base, **changes})

    def test_rejects_oversized_arrays_before_reading_image_bytes(self):
        backing = np.zeros(1, np.uint8)
        images = np.lib.stride_tricks.as_strided(
            backing, shape=(2, 12, 4096, 4096, 3), strides=(0, 0, 0, 0, 0)
        )
        with self.assertRaisesRegex(ValueError, "oversized"):
            prepare_observed_flow(
                images,
                [0.0, 0.04],
                [0, 1],
                view_ids=list(range(12)),
                train_indices=[i for i in range(12) if i not in (0, 10)],
                heldout_indices=[0, 10],
                latest_arrived_time_seconds=0.04,
            )


if __name__ == "__main__":
    unittest.main()
