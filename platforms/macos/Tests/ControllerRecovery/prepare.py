"""Use the production controller with a headless host and an isolated socket.

Only the framework import and session factory change. Event handling, queueing,
failure recovery and rendering remain the production implementation.
"""
from pathlib import Path
import sys

source = Path(sys.argv[1]).read_text()
for before, after in (
    ("import InputMethodKit\n", ""),
    ("EngineHost.shared.session", "ControllerTestHost.session"),
):
    if source.count(before) != 1:
        raise SystemExit(f"expected one controller dependency: {before!r}")
    source = source.replace(before, after)
Path(sys.argv[2]).write_text(source)
