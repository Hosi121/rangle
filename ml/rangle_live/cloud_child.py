"""Private training subprocess; the cloud supervisor owns publication and lifetime."""

import json
import signal
import sys
from pathlib import Path
from types import SimpleNamespace

from .runner import log, run


def main():
    args = SimpleNamespace(**json.loads(Path(sys.argv[1]).read_bytes()))

    def cancel(_number, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, cancel)
    try:
        run(args)
    except KeyboardInterrupt:
        log("live_run_cancelled")
        return 130
    except Exception as error:  # noqa: BLE001 - one structured child boundary
        log("live_run_failed", severity="ERROR", error=str(error))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
