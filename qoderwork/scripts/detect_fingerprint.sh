#!/usr/bin/env bash
# Print the real Qoder CN device fingerprint as one-line JSON for the
# `checkin_fingerprint` plugin config. macOS only.
#
# The CN daily-credit campaign (/sash/api/v1/me/campaigns) answers an unknown
# Cosy-MachineToken with HTTP 200 and an empty campaign list instead of an
# error, so the plugin needs a real device token to reach the activity.
# Without one the panel reports "当前账号无可用签到活动".
#
# WARNING: the output contains a device token. Do not share it.
set -euo pipefail

APP_DIR="${QODER_CN_APP_DIR:-$HOME/Library/Application Support/com.qodercn.app.stable}"
APP_BUNDLE="${QODER_CN_APP_BUNDLE:-/Applications/Qoder CN.app}"
RUNTIME_INFO="${QODER_CN_RUNTIME_INFO:-}"

if [[ -z "$RUNTIME_INFO" ]]; then
  # The CN desktop client ships its own runtime-info helper; fall back to the
  # one the global client drops into ~/.qoder/.bin.
  for candidate in \
    "$APP_BUNDLE/Contents/Resources/umid/runtime-info" \
    "$HOME/.qoder/.bin"/runtime-info-darwin-*; do
    if [[ -x "$candidate" ]]; then
      RUNTIME_INFO="$candidate"
      break
    fi
  done
fi
if [[ -z "$RUNTIME_INFO" || ! -x "$RUNTIME_INFO" ]]; then
  echo "runtime-info binary not found; set QODER_CN_RUNTIME_INFO" >&2
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
