# -*- mode: python ; coding: utf-8 -*-
"""PyInstaller build for the Windows executable.

Build (on Windows — PyInstaller cannot cross-compile):
    uv run pyinstaller packaging/dnd-companion.spec

Two things here are load-bearing:

* `datas` ships the whole `web/` tree, including the vendored dice and fonts.
  The app is meant to work with the router unplugged, so nothing may be fetched
  at runtime; if it isn't in this list it doesn't exist in the build.
* `hiddenimports` lists uvicorn's protocol and loop backends. uvicorn picks them
  by string at runtime, so static analysis cannot see them and the build would
  otherwise fail on first connection rather than at build time.
"""

import os

# Paths in a spec resolve against the working directory, not the spec file, so
# anchor everything to SPECPATH (injected by PyInstaller) and the build works
# the same however it was launched.
ROOT = os.path.dirname(os.path.abspath(SPECPATH))

block_cipher = None

a = Analysis(
    [os.path.join(SPECPATH, "launch.py")],
    pathex=[ROOT],
    binaries=[],
    datas=[(os.path.join(ROOT, "web"), "web")],
    hiddenimports=[
        "uvicorn.logging",
        "uvicorn.loops.auto",
        "uvicorn.loops.asyncio",
        "uvicorn.protocols.auto",
        "uvicorn.protocols.http.auto",
        "uvicorn.protocols.http.h11_impl",
        "uvicorn.protocols.websockets.auto",
        "uvicorn.protocols.websockets.websockets_impl",
        "uvicorn.lifespan.on",
        "uvicorn.lifespan.off",
    ],
    hookspath=[],
    runtime_hooks=[],
    # Nothing here needs a scientific stack; leaving these in would multiply the
    # download size for a file that gets passed around over chat.
    excludes=["numpy", "matplotlib", "PIL", "pytest", "setuptools", "pip"],
    win_no_prefer_redirects=False,
    win_private_assemblies=False,
    cipher=block_cipher,
    noarchive=False,
)
pyz = PYZ(a.pure, a.zipped_data, cipher=block_cipher)

exe = EXE(
    pyz,
    a.scripts,
    a.binaries,
    a.zipfiles,
    a.datas,
    [],
    name="DnD Table Companion",
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=False,          # UPX-packed exes trip antivirus heuristics far more often
    runtime_tmpdir=None,
    console=False,      # the Tk window is the interface; there is no terminal
    disable_windowed_traceback=False,
    argv_emulation=False,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
)
