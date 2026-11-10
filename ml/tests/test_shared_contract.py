"""The same bounded wire fixtures are consumed by Python, Go and JavaScript."""

import hashlib
import json
import unittest
from pathlib import Path

from rangle_training.packing import decode_temporal, temporal_header, unpack_temporal

ROOT = Path(__file__).resolve().parents[2] / "testdata/contracts/v1"


class SharedWireContracts(unittest.TestCase):
    def test_production_temporal_decoder(self):
        corpus = json.loads((ROOT / "cases.json").read_text())
        for case in corpus["cases"]:
            if case["profile"] != "rgs":
                continue
            with self.subTest(case=case["id"]):
                payload = (ROOT / case["file"]).read_bytes()
                if case.get("recipe", {}).get("kind") == "pad-to-size":
                    payload = payload.ljust(case["recipe"]["bytes"], b"\0")
                if not case["accept"]:
                    with self.assertRaises((ValueError, OSError, EOFError)):
                        unpack_temporal(payload)
                    continue
                unpack_temporal(payload)
                _, frames, fps, _, _ = temporal_header(payload)
                expected = case["normalized"]
                self.assertEqual(fps, expected["fps"])
                self.assertAlmostEqual((frames - 1) / fps, expected["end"], places=12)
                self.assertEqual(
                    hashlib.sha256(decode_temporal(payload)).hexdigest(),
                    expected["raw_sha256"],
                )


if __name__ == "__main__":
    unittest.main()
