#!/usr/bin/env bash
# Start the table companion. Players join from the QR code it prints.
set -euo pipefail
cd "$(dirname "$0")"
exec uv run python -m server.main "$@"
