# Start the table companion on Windows, from a checkout.
# Players join from the QR code in the window it opens.
#
# This is the development launcher. For a machine that just wants to play,
# use the built executable instead — see the Windows section of README.md.
$ErrorActionPreference = "Stop"
Set-Location -Path $PSScriptRoot

# The QR is drawn with block characters that cp1252 cannot encode, and Windows
# uses the locale encoding whenever output is redirected rather than a console.
$env:PYTHONUTF8 = "1"

uv run python -m server.main @args
