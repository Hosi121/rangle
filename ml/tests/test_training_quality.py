import unittest

import numpy as np
from rangle_training.data import Dataset, training_activity
from rangle_training.train import parse_args
from test_temporal import temporal_fixture


class QualityContractTests(unittest.TestCase):
    def test_activity_uses_training_pixels_only(self):
        values = temporal_fixture()
        values["images"][1, 0, 10:20, 10:20] = 255
        before = training_activity(Dataset(**values))
        values["images"][:, 3] = np.random.default_rng(1).integers(
            0, 256, values["images"][:, 3].shape, dtype=np.uint8
        )
        after = training_activity(Dataset(**values))
        self.assertEqual(set(before), {0, 1, 2})
        for view in before:
            np.testing.assert_array_equal(before[view], after[view])
        self.assertEqual(float(before[0][15, 15]), 1)
        self.assertEqual(float(before[0][0, 0]), 0)

    def test_activity_matches_temporal_rgb_variance(self):
        values = temporal_fixture()
        values["images"][:, 0, 0, 0] = np.array([[0, 0, 0], [10, 20, 30], [20, 40, 60]])
        maps = training_activity(Dataset(**values))
        expected = np.std(values["images"][:, 0].astype(np.float32) / 255, axis=0)
        expected = np.clip(np.sqrt((expected**2).mean(axis=-1)) / 0.05, 0, 1)
        np.testing.assert_allclose(maps[0], expected, atol=1e-6)

    def test_profiles_are_explicit_and_static_rejects_candidates(self):
        flags = [
            "--input-uri",
            "gs://example/input.npz",
            "--input-sha256",
            "a" * 64,
            "--output-prefix",
            "gs://example/generated/" + "b" * 32 + "/",
            "--job-id",
            "b" * 32,
            "--deadline",
            "2026-11-09T12:00:00Z",
            "--title",
            "Example",
            "--source",
            "https://example.com",
            "--license",
            "CC-BY-4.0",
        ]
        self.assertEqual(parse_args(flags).quality_profile, "baseline")
        for profile in ("motion-prior", "local-lifetime"):
            self.assertEqual(
                parse_args(
                    flags + ["--mode", "dynamic", "--quality-profile", profile]
                ).quality_profile,
                profile,
            )
            with self.assertRaises(ValueError):
                parse_args(flags + ["--quality-profile", profile])


if __name__ == "__main__":
    unittest.main()
