"""Incremental 4DGS from paced, arrived calibrated camera videos (local GPU only)."""

import argparse
from pathlib import Path


def parse_args(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-manifest", type=Path, required=True)
    parser.add_argument("--source-root", type=Path, required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    parser.add_argument("--cuda-extension", type=Path)
    parser.add_argument("--session-id", default="basketball-live")
    parser.add_argument("--title", default="バスケットボール ライブ検証")
    parser.add_argument("--bootstrap-steps", type=int, default=300)
    parser.add_argument("--steps-per-update", type=int, default=300)
    parser.add_argument("--decode-workers", type=int, metavar="1..32", default=8)
    parser.add_argument("--source-seconds", type=float, default=10)
    parser.add_argument("--seed-available-after", type=float, required=True)
    parser.add_argument("--max-runtime-seconds", type=int, default=180)
    args = parser.parse_args(argv)
    if (
        not 1 <= args.bootstrap_steps <= 1000
        or not 1 <= args.steps_per_update <= 2000
        or not 1 <= args.decode_workers <= 32
        or not 0 < args.source_seconds <= 1800
        or not 0 <= args.seed_available_after < args.source_seconds
        or not 10 <= args.max_runtime_seconds <= 1800
    ):
        parser.error(
            "Finite source, seed-prior timing and iteration/runtime budgets are required"
        )
    return args


def main(argv=None):
    args = parse_args(argv)
    from .runner import log, run

    try:
        run(args)
    except KeyboardInterrupt:
        log("live_run_cancelled")
        raise SystemExit(130) from None
    except Exception as error:  # noqa: BLE001 - command boundary logs one structured failure
        log(
            "live_run_failed",
            severity="ERROR",
            error=str(error),
            cause=str(error.__cause__) if error.__cause__ else None,
        )
        raise SystemExit(1) from None


if __name__ == "__main__":
    main()
