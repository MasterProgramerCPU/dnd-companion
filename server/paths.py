"""Where the app's files live — in a checkout, and inside a frozen build.

There are two anchors here and confusing them loses people's campaigns.

*Bundled assets* (everything under `web/`) ship read-only inside the executable.
PyInstaller unpacks them to a scratch directory and **deletes it on exit**, so
that is the right home for the dice and the stylesheets and the wrong home for
anything the DM typed.

*User data* (the campaign files, handout pictures) has to outlive the process,
so it goes to the per-user location the OS guarantees is writable — beside the
source tree when running from a checkout, so a developer's data stays where the
README says it is and the systemd service on Linux sees no change at all.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

APP_NAME = "DnDCompanion"

# `frozen` alone is set by several bundlers; `_MEIPASS` is what tells us the
# assets were unpacked somewhere other than next to the source.
FROZEN = getattr(sys, "frozen", False) and hasattr(sys, "_MEIPASS")

_SOURCE_ROOT = Path(__file__).resolve().parent.parent


def bundled(*parts: str) -> Path:
    """A read-only file that shipped with the app."""
    root = Path(sys._MEIPASS) if FROZEN else _SOURCE_ROOT  # type: ignore[attr-defined]
    return root.joinpath(*parts)


def default_data_dir() -> Path:
    """Where campaigns live when DND_DATA_DIR hasn't been set."""
    # From a checkout, keep the old behaviour exactly: ./data next to the code.
    if not FROZEN:
        return _SOURCE_ROOT / "data"

    if sys.platform == "win32":
        base = os.environ.get("LOCALAPPDATA") or (Path.home() / "AppData" / "Local")
    elif sys.platform == "darwin":
        base = Path.home() / "Library" / "Application Support"
    else:
        base = os.environ.get("XDG_DATA_HOME") or (Path.home() / ".local" / "share")
    return Path(base) / APP_NAME
