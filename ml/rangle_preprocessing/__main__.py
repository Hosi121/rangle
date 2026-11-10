"""Convert a local calibrated-media manifest to bounded trainer NPZ + metadata."""

import argparse
import json
from pathlib import Path

from .pipeline import prepare_dataset


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument(
        "--source-root", type=Path, help="Local root for manifest-relative input paths"
    )
    args = parser.parse_args(argv)
    result = prepare_dataset(args.manifest, args.output, source_root=args.source_root)
    print(
        json.dumps(
            {
                key: result[key]
                for key in (
                    "mode",
                    "npz_sha256",
                    "npz_bytes",
                    "images_shape",
                    "frame_rate",
                    "points",
                )
            },
            allow_nan=False,
        )
    )


if __name__ == "__main__":
    main()
