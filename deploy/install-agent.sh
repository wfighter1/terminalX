#!/usr/bin/env bash
# Install the terminalX agent on a Linux (or macOS) controlled machine:
# put the binary in ~/.local/bin, pair it with your relay, and register it to
# start at login and stay running after logout.
#
#   deploy/install-agent.sh --relay https://tx.example.com --code XXXX-XXXX [--name box]
#
# The binary comes from, in order: --binary PATH, ./bin/tx-agent-linux-<arch>,
# ./bin/tx-agent, or a local `go build` when the repository and Go are present.
#
# Re-run it after moving the binary or changing your PATH: the systemd unit
# records both.
set -euo pipefail

RELAY=""; CODE=""; NAME=""; BINARY=""
while [ $# -gt 0 ]; do
  case "$1" in
    --relay) RELAY="$2"; shift 2 ;;
    --code) CODE="$2"; shift 2 ;;
    --name) NAME="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done
if [ -z "$RELAY" ] || [ -z "$CODE" ]; then
  echo "error: --relay and --code are required (get the code from the web console: 设备 → 添加设备)" >&2
  exit 2
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARCH="$(uname -m)"; case "$ARCH" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; esac

if [ -z "$BINARY" ]; then
  for cand in "$ROOT/bin/tx-agent-linux-$ARCH" "$ROOT/bin/tx-agent"; do
    if [ -x "$cand" ]; then BINARY="$cand"; break; fi
  done
fi
if [ -z "$BINARY" ]; then
  if ! command -v go >/dev/null 2>&1; then
    echo "error: no prebuilt binary in $ROOT/bin and no Go toolchain to build one; pass --binary PATH" >&2
    exit 1
  fi
  echo "[install] building tx-agent from source"
  (cd "$ROOT" && go build -trimpath -o bin/tx-agent ./cmd/tx-agent)
  BINARY="$ROOT/bin/tx-agent"
fi

DEST="${TX_BIN_DIR:-$HOME/.local/bin}"
mkdir -p "$DEST"
install -m 0755 "$BINARY" "$DEST/tx-agent"
echo "[install] installed $DEST/tx-agent"
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "[install] note: $DEST is not on your PATH; add it to ~/.profile" ;;
esac

PAIR_ARGS=(pair --relay "$RELAY" --code "$CODE")
if [ -n "$NAME" ]; then PAIR_ARGS+=(--name "$NAME"); fi
"$DEST/tx-agent" "${PAIR_ARGS[@]}"

# The unit records the PATH of this shell, so the agent can find claude/codex.
"$DEST/tx-agent" install
echo
"$DEST/tx-agent" doctor | sed -n '/^autostart:/,+2p'
echo "[install] done. Logs: journalctl --user -u tx-agent.service -f"
