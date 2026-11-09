import gzip
import hashlib
import json
import shutil
import struct
import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import numpy as np
from rangle_training.packing import (
    MAX_TEMPORAL_BYTES,
    compress_temporal,
    compress_temporal_rle,
    compress_temporal_smallest,
    decode_temporal,
)


def wire_fixture(count=4099):
    # Byte patterns rather than float arithmetic exercise every stored bit,
    # canonical row ordering, and input spanning several gzip output chunks.
    rows = np.random.default_rng(21).integers(0, 256, (count, 56), dtype=np.uint8)
    rows[:, 24:28] = 0  # Include IDs invisible at the initial instant.
    header = struct.pack("<8sIIIffI", b"RANGL4D1", count, 56, 20, 25, 0.76, 0)
    return header + rows.tobytes()


class TemporalCompressionTests(unittest.TestCase):
    def test_opt_in_encoder_preserves_every_bit_and_is_deterministic(self):
        for count in (1, 4099):
            with self.subTest(count=count):
                raw = wire_fixture(count)
                encoded = compress_temporal_rle(raw)
                self.assertEqual(encoded[:8], b"RANGL4Z1")
                self.assertEqual(encoded[8:32], raw[8:32])
                self.assertEqual(encoded[36:40], b"\0" * 4)  # No wall-clock mtime.
                self.assertEqual(compress_temporal_rle(raw), encoded)
                self.assertEqual(decode_temporal(encoded), raw)
                self.assertEqual(decode_temporal(raw), raw)
                with self.assertRaises(ValueError):
                    compress_temporal_rle(encoded)

    def test_old_default_strategy_z1_remains_readable(self):
        raw = wire_fixture()
        count = (len(raw) - 32) // 56
        columns = (
            np.frombuffer(raw, np.uint8, offset=32).reshape(count, 56).T.tobytes()
        )
        old = (
            b"RANGL4Z1" + raw[8:32] + gzip.compress(columns, compresslevel=9, mtime=0)
        )
        self.assertEqual(compress_temporal(raw), old)
        self.assertEqual(decode_temporal(old), raw)

    def test_smallest_never_exceeds_either_encoder_on_different_wire_patterns(self):
        random_rows = wire_fixture()
        repeated_rows = random_rows[:32] + random_rows[32:88] * 4099
        for raw in (random_rows, repeated_rows):
            with self.subTest(repeated=raw is repeated_rows):
                default, rle = compress_temporal(raw), compress_temporal_rle(raw)
                smallest = compress_temporal_smallest(raw)
                self.assertEqual(len(smallest), min(len(default), len(rle)))
                expected = rle if len(rle) < len(default) else default
                self.assertEqual(smallest, expected)
                self.assertEqual(decode_temporal(smallest), raw)

    def test_equal_size_selection_keeps_the_existing_default_bytes(self):
        raw = wire_fixture(1)
        default = compress_temporal(raw)
        alternate = bytearray(default)
        alternate[41] ^= 1  # A different, valid gzip OS header with equal length.
        alternate = bytes(alternate)
        self.assertEqual(decode_temporal(alternate), raw)
        with (
            patch("rangle_training.packing.compress_temporal", return_value=default),
            patch(
                "rangle_training.packing.compress_temporal_rle", return_value=alternate
            ),
        ):
            self.assertIs(compress_temporal_smallest(raw), default)

    def test_corrupt_or_overexpanding_gzip_is_rejected(self):
        encoded = compress_temporal_rle(wire_fixture(2))
        bad_crc = bytearray(encoded)
        bad_crc[-8] ^= 1
        for bad in (bytes(bad_crc), encoded[:-4], encoded + b"invalid"):
            with (
                self.subTest(size=len(bad)),
                self.assertRaises((ValueError, OSError, EOFError)),
            ):
                decode_temporal(bad)
        for count in (1, 3, 1_000_000):
            bad = bytearray(encoded)
            struct.pack_into("<I", bad, 8, count)
            with self.subTest(count=count), self.assertRaises(ValueError):
                decode_temporal(bytes(bad))

    def test_new_encoders_keep_input_caps_and_reject_packed_input(self):
        raw = wire_fixture(1)
        oversized = raw + bytes(MAX_TEMPORAL_BYTES + 1 - len(raw))
        for encode in (compress_temporal_rle, compress_temporal_smallest):
            for invalid in (raw[:-1], oversized, compress_temporal(raw)):
                with (
                    self.subTest(encoder=encode.__name__),
                    self.assertRaises(ValueError),
                ):
                    encode(invalid)

    @unittest.skipUnless(shutil.which("node"), "Node codec oracle is required")
    def test_python_and_node_packer_decode_to_the_same_raw_sha(self):
        root = Path(__file__).resolve().parents[2]
        codec = root / "apps/web/viewer/src/temporal-codec.js"
        packer = root / "apps/web/viewer/pack-temporal.mjs"
        raw = wire_fixture()
        expected_sha = hashlib.sha256(raw).hexdigest()
        with tempfile.TemporaryDirectory() as directory:
            source, python_output, node_output = (
                Path(directory) / name for name in ("raw.rgs", "python.rgs", "node.rgs")
            )
            source.write_bytes(raw)
            python_output.write_bytes(compress_temporal_rle(raw))
            summary = json.loads(
                subprocess.check_output(
                    ["node", str(packer), str(source), str(node_output), "--strategy=rle"],
                    text=True,
                )
            )
            self.assertEqual(summary["decodedSHA256"], expected_sha)
            self.assertEqual(summary["strategy"], "rle")
            self.assertEqual(summary["encoding"], "byte-shuffle-gzip")
            self.assertEqual(decode_temporal(node_output.read_bytes()), raw)
            script = """import fs from 'node:fs';
                import {createHash} from 'node:crypto';
                const {decodeTemporal} = await import(process.argv[1]);
                const b = fs.readFileSync(process.argv[2]);
                const data = b.buffer.slice(b.byteOffset,b.byteOffset+b.byteLength);
                const raw = await decodeTemporal(data);
                console.log(createHash('sha256').update(Buffer.from(raw)).digest('hex'));"""
            actual_sha = subprocess.check_output(
                [
                    "node", "--input-type=module", "-e", script,
                    codec.as_uri(), str(python_output),
                ],
                text=True,
            ).strip()
            self.assertEqual(actual_sha, expected_sha)


if __name__ == "__main__":
    unittest.main()
