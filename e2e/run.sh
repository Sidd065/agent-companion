#!/usr/bin/env bash
set -euo pipefail

export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
export PATH="$HOME/.local/bin:$HOME/.local/go/bin:$PATH"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

MODE="all"
TIER=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --all)
      MODE="all"
      shift
      ;;
    --tier)
      if [[ $# -lt 2 ]]; then
        echo "error: --tier requires an argument (1, 2, 3, 4, or 5)" >&2
        exit 2
      fi
      MODE="tier"
      TIER="$2"
      shift 2
      ;;
    --tier=*)
      MODE="tier"
      TIER="${1#--tier=}"
      shift
      ;;
    *)
      echo "error: unknown argument: $1 (supported: --all, --tier 1|2|3|4|5)" >&2
      exit 2
      ;;
  esac
done

if [[ "$MODE" == "tier" ]]; then
  case "$TIER" in
    1|2|3|4|5) ;;
    *)
      echo "error: invalid tier '$TIER' (must be 1, 2, 3, 4, or 5)" >&2
      exit 2
      ;;
  esac
fi

# Short scratch root outside /tmp so CODEX_HOME is never in /tmp AND
# Unix domain sockets (agentc.sock, tmux.sock) stay well below 108 bytes.
SHORT_ID="$(printf %x "$(date +%s)" | tail -c 6)$$"
E2E_ROOT="$HOME/.cache/agce2e/$SHORT_ID"
mkdir -p "$E2E_ROOT/tmp"

cleanup() {
  set +eu
  if [[ -n "${E2E_ROOT:-}" && -d "$E2E_ROOT" ]]; then
    while IFS= read -r -d '' sock; do
      tmux -S "$sock" kill-server >/dev/null 2>&1 || true
    done < <(find "$E2E_ROOT" -name "tmux.sock" -print0 2>/dev/null || true)
    rm -rf "$E2E_ROOT"
    rmdir "$HOME/.cache/agce2e" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

export AGENTC_E2E_ROOT="$E2E_ROOT"
export TMPDIR="$E2E_ROOT/tmp"

cd "$SCRIPT_DIR"

RACE_FLAGS=()
if [[ "${AGENTC_E2E_RACE:-0}" == "1" ]]; then
  RACE_FLAGS=(-race)
fi

run_tier() {
  local t="$1"
  local timeout_dur="6m"
  if [[ "$t" == "4" ]]; then
    timeout_dur="15m"
  elif [[ "$t" == "5" ]]; then
    timeout_dur="10m"
  fi
  echo "=== Running E2E Tier ${t} (timeout ${timeout_dur}) ==="
  go test "${RACE_FLAGS[@]}" -count=1 -v -timeout "$timeout_dur" -run "^TestTier${t}_" .
}

if [[ "$MODE" == "tier" ]]; then
  run_tier "$TIER"
else
  STATUS=0
  for t in 1 2 3 4 5; do
    if ! run_tier "$t"; then
      STATUS=1
    fi
  done
  exit "$STATUS"
fi
