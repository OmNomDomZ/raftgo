#!/usr/bin/env bash
set -euo pipefail
SCENARIO_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
exec python3 "$SCENARIO_DIR/run.py" 1
