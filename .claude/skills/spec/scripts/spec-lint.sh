#!/bin/bash
# Validate new-format SPEC metadata and sections; reject obsolete INDEX files. Python 3.9+.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$SCRIPT_DIR/spec_workflow.py" lint "$@"
