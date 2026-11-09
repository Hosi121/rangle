"""Invoke the worker's real Go semantic gate; this is not a Python validator.

Called by the Go contract test with a freshly built production gate. Standard
library only, so the Go CI job does not need the ML dependency environment.
"""

import importlib.util
import json
import sys
from pathlib import Path
from types import SimpleNamespace

source = Path(__file__).resolve().parents[2] / "scripts/publish-live.py"
spec = importlib.util.spec_from_file_location("live_publisher_contract", source)
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)
gate, index, origin, root = sys.argv[1:]
try:
    publisher.verify(
        SimpleNamespace(gate=gate, origin=origin),
        json.loads(Path(index).read_bytes()),
        root or None,
    )
except publisher.PublishError:
    sys.exit(1)
