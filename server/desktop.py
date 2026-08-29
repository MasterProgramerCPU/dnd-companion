"""The window the DM actually sees.

A frozen build has no terminal, so this small window *is* the app: it shows the
QR the players scan, it says where the campaign file lives, and closing it stops
the server. That is the whole lifecycle — no service, no autostart, nothing left
running after the session.

Tk owns the main thread, so uvicorn serves from a worker. uvicorn skips its
signal handlers when it isn't on the main thread, so there is nothing to
work around.
"""

from __future__ import annotations

import os
import subprocess
import sys
import threading
import tkinter as tk
import webbrowser
from tkinter import font as tkfont
from tkinter import messagebox

BG = "#1b1714"
GOLD = "#d9b168"
TEXT = "#efe6d8"
MUTED = "#9c9083"


def _port_is_free(port: int) -> bool:
    """Bind-test before uvicorn does, so a clash can be explained in words
    rather than as a stack trace nobody sees."""
    import socket

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            s.bind(("0.0.0.0", port))
            return True
        except OSError:
            return False


def _open_folder(path) -> None:
    if sys.platform == "win32":
        os.startfile(path)  # type: ignore[attr-defined]
    elif sys.platform == "darwin":
        subprocess.Popen(["open", str(path)])
    else:
        subprocess.Popen(["xdg-open", str(path)])


def _draw_qr(canvas: tk.Canvas, url: str, size: int) -> None:
    import qrcode

    qr = qrcode.QRCode(border=2)
    qr.add_data(url)
    matrix = qr.get_matrix()
    module = max(1, size // len(matrix))
    pad = (size - module * len(matrix)) // 2
    canvas.create_rectangle(0, 0, size, size, fill="white", outline="")
    for y, row in enumerate(matrix):
        for x, dark in enumerate(row):
            if dark:
                canvas.create_rectangle(
                    pad + x * module, pad + y * module,
                    pad + (x + 1) * module, pad + (y + 1) * module,
                    fill="black", outline="",
                )


def run_windowed(app, url: str, port: int, campaign: str, data_dir) -> None:
    import uvicorn

    if not _port_is_free(port):
        messagebox.showerror(
            "D&D Table Companion",
            f"Port {port} is already in use.\n\n"
            "The app is probably already running — check for another window, "
            "or the icon in your taskbar.",
        )
        return

    config = uvicorn.Config(app, host="0.0.0.0", port=port, log_level="warning")
    server = uvicorn.Server(config)
    thread = threading.Thread(target=server.run, name="uvicorn", daemon=True)
    thread.start()

    root = tk.Tk()
    root.title("D&D Table Companion")
    root.configure(bg=BG)
    root.resizable(False, False)

    head = tkfont.Font(family="Segoe UI", size=11, weight="bold")
    body = tkfont.Font(family="Segoe UI", size=10)
    mono = tkfont.Font(family="Consolas", size=13, weight="bold")

    wrap = tk.Frame(root, bg=BG, padx=22, pady=18)
    wrap.pack()

    tk.Label(wrap, text=campaign, font=head, bg=BG, fg=GOLD).pack()
    tk.Label(wrap, text="Players scan this to join", font=body, bg=BG, fg=MUTED).pack(pady=(0, 10))

    canvas = tk.Canvas(wrap, width=240, height=240, bg="white", highlightthickness=0)
    canvas.pack()
    _draw_qr(canvas, url, 240)

    # 28 characters is the longest this can get (http://255.255.255.255:65535),
    # and a clipped address is worse than useless — someone types it wrong.
    entry = tk.Entry(wrap, font=mono, justify="center", relief="flat",
                     bg="#2a2320", fg=TEXT, readonlybackground="#2a2320", width=30)
    entry.insert(0, url)
    entry.configure(state="readonly")
    entry.pack(pady=(12, 4), ipady=5)
    tk.Label(wrap, text="…or type that in, on the same Wi-Fi", font=body, bg=BG, fg=MUTED).pack()

    row = tk.Frame(wrap, bg=BG)
    row.pack(pady=(14, 0))

    def button(parent, text, command, accent=False):
        return tk.Button(
            parent, text=text, command=command, font=body, relief="flat", cursor="hand2",
            bg=GOLD if accent else "#332b26", fg="#1b1714" if accent else TEXT,
            activebackground=GOLD if accent else "#3f3630", padx=12, pady=6, borderwidth=0,
        )

    button(row, "Open DM console", lambda: webbrowser.open(f"{url}/dm"), accent=True).pack(side="left", padx=4)
    button(row, "Campaign files", lambda: _open_folder(data_dir)).pack(side="left", padx=4)

    def quit_app() -> None:
        server.should_exit = True
        thread.join(timeout=5)
        root.destroy()

    button(wrap, "Stop and quit", quit_app).pack(pady=(10, 0))
    tk.Label(wrap, text="Closing this window ends the session.",
             font=body, bg=BG, fg=MUTED).pack(pady=(8, 0))

    root.protocol("WM_DELETE_WINDOW", quit_app)
    root.mainloop()
