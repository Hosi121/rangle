import struct
import unittest

import numpy as np
from rangle_training.packing import TEMPORAL, pack_temporal
from rangle_training.temporal import compression_candidate_models
from rangle_training.train import parse_args
from test_temporal import model_fixture


class CompressionEvaluationTests(unittest.TestCase):
    def test_candidate_roundtrips_keep_reference_and_static_bytes(self):
        arrays = list(model_fixture())
        arrays[0][0, 0] = 1.123456
        arrays[1][0, 0] = 0.123456
        raw = pack_temporal(*arrays, frame_count=3, frame_rate=25)
        original = bytes(raw)
        candidates = compression_candidate_models(raw)
        self.assertEqual(raw, original)
        self.assertEqual(len(candidates), 2)
        source = np.frombuffer(raw, dtype=TEMPORAL, offset=32)
        for metadata, candidate_arrays in candidates:
            self.assertFalse(metadata["published"])
            self.assertTrue(metadata["primitive_ids_preserved"])
            self.assertEqual(metadata["status"], "evaluated")
            self.assertEqual(metadata["raw_bytes"], len(raw))
            self.assertGreater(metadata["compressed_bytes"], 32)
            self.assertEqual(len(candidate_arrays[0]), len(arrays[0]))
            # RGB/quat are decoded from unchanged 8-bit storage.
            np.testing.assert_array_equal(
                candidate_arrays[3], source["rgba"][:, :3].astype(np.float32) / 255
            )
            np.testing.assert_array_equal(
                candidate_arrays[5], arrays[5].astype(np.float16).astype(np.float32)
            )
        self.assertNotEqual(candidates[0][1][0][0, 0], arrays[0][0, 0])
        np.testing.assert_array_equal(candidates[1][1][0], arrays[0])
        np.testing.assert_array_equal(
            candidates[1][1][1], arrays[1].astype(np.float16).astype(np.float32)
        )

    def test_overflow_candidate_is_reported_and_never_adopted(self):
        arrays = list(model_fixture())
        arrays[0][0, 0] = 100_000
        raw = pack_temporal(*arrays, frame_count=3, frame_rate=25)
        candidates = compression_candidate_models(raw)
        self.assertEqual(candidates[0][0]["status"], "rejected")
        self.assertIsNone(candidates[0][1])
        self.assertEqual(candidates[1][0]["status"], "evaluated")
        self.assertEqual(struct.unpack_from("<f", raw, 32)[0], 100_000)

    def test_evaluation_flag_is_explicit_and_dynamic_only(self):
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
        self.assertFalse(parse_args(flags).evaluate_compression)
        self.assertTrue(
            parse_args(
                flags + ["--mode", "dynamic", "--evaluate-compression"]
            ).evaluate_compression
        )
        with self.assertRaises(ValueError):
            parse_args(flags + ["--evaluate-compression"])


if __name__ == "__main__":
    unittest.main()
