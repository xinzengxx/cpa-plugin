#!/usr/bin/env bash
# Print the real Qoder device fingerprint as one-line JSON for the
# `checkin_fingerprint` plugin config. macOS only.
#
# The Qoder global daily-credit campaign is only issued when the request
# carries a real device fingerprint. Derived values are accepted for some
# endpoints but the CLAIM_BENEFIT campaign stays hidden.
#
# WARNING: the output contains a device token. Do not share it.
set -euo pipefail

APP_DIR="${QODER_APP_DIR:-$HOME/Library/Application Support/com.qoder.app.stable}"
RUNTIME_INFO="${QODER_RUNTIME_INFO:-}"

if [[ -z "$RUNTIME_INFO" ]]; then
  RUNTIME_INFO=$(ls "$HOME/.qoder/.bin"/runtime-info-darwin-* 2>/dev/null | head -1 || true)
fi
if [[ ! -x "$RUNTIME_INFO" ]]; then
  echo "runtime-info binary not found; set QODER_RUNTIME_INFO" >&2
  exit 1
fi
if [[ ! -f "$APP_DIR/auth.machine-id" ]]; then
  echo "auth.machine-id not found under: $APP_DIR" >&2
  exit 1
fi

MID=$(tr -d '[:space:]' < "$APP_DIR/auth.machine-id")
RI=$("$RUNTIME_INFO")

python3 - "$MID" "$RI" <<'PY'
import json, sys
mid, raw = sys.argv[1], sys.argv[2]
ri = json.loads(raw)
fp = {
    "machine_id": mid,
    "machine_token": ri["machineToken"],
    "machine_type": ri["machineType"],
    "machine_code": ri.get("machineCode", mid),
}
print(json.dumps(fp, separators=(",", ":")))
PY
