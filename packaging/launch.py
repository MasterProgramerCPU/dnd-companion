"""Entry point for the frozen Windows build.

A windowed executable has nowhere to print a traceback, so an exception on the
way up would just make the app vanish with no explanation. Everything is wrapped
so a failure at least says something a DM can read out over the table.
"""

from __future__ import annotations

import multiprocessing
import sys


def _report(exc: BaseException) -> None:
    import traceback

    detail = "".join(traceback.format_exception(type(exc), exc, exc.__traceback__))
    try:
        import tkinter as tk
        from tkinter import scrolledtext

        root = tk.Tk()
        root.title("D&D Table Companion — failed to start")
        tk.Label(root, text="The app could not start.", padx=16, pady=10).pack()
        box = scrolledtext.ScrolledText(root, width=100, height=22)
        box.insert("1.0", detail)
        box.configure(state="disabled")
        box.pack(padx=12, pady=(0, 12))
        root.mainloop()
    except Exception:
        if sys.stderr is not None:
            sys.stderr.write(detail)


def main() -> None:
    # PyInstaller re-executes the bundle for each child process; without this a
    # stray spawn would open a second window instead of a worker.
    multiprocessing.freeze_support()
    try:
        from server.main import main as serve

        serve()
    except BaseException as exc:  # noqa: BLE001 - last line before the app disappears
        if isinstance(exc, (KeyboardInterrupt, SystemExit)):
            raise
        _report(exc)
        raise SystemExit(1)


if __name__ == "__main__":
    main()
