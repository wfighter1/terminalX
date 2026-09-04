#!/usr/bin/env bash
# Build everything: web console → embedded into tx-relay; tx-agent for the
# host platform and for Windows amd64.
#
#   scripts/build.sh                 # full build
#   SKIP_WEB=1 scripts/build.sh      # reuse the existing internal/webdist/dist
#   VERSION=v0.1.0 scripts/build.sh  # stamp a version (main.version)
#
# Outputs go to bin/ (git-ignored).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

export GOTOOLCHAIN="${GOTOOLCHAIN:-auto}"
export CGO_ENABLED=0
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
LDFLAGS="-s -w -X main.version=${VERSION}"
WEBDIST="internal/webdist/dist"

log() { printf '\033[1;34m[build]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[build]\033[0m %s\n' "$*" >&2; }

# ---- 1. web console ---------------------------------------------------------
if [ "${SKIP_WEB:-0}" = "1" ]; then
  log "SKIP_WEB=1: keeping existing ${WEBDIST}"
else
  if ! command -v npm >/dev/null 2>&1; then
    warn "npm not found; skipping the web build (relay will embed the placeholder page)"
  else
    log "building web console (web/)"
    (cd web && npm ci --no-audit --no-fund && npm run build)
    log "copying web/dist → ${WEBDIST}"
    rm -rf "${WEBDIST}"
    mkdir -p "${WEBDIST}"
    if command -v rsync >/dev/null 2>&1; then
      rsync -a --delete web/dist/ "${WEBDIST}/"
    else
      cp -R web/dist/. "${WEBDIST}/"
    fi
  fi
fi
if [ ! -f "${WEBDIST}/index.html" ]; then
  echo "error: ${WEBDIST}/index.html missing (the placeholder is tracked in git; run 'git checkout -- ${WEBDIST}')" >&2
  exit 1
fi

# ---- 2. relay -----------------------------------------------------------------
mkdir -p bin
log "go build tx-relay (${VERSION})"
go build -trimpath -ldflags "${LDFLAGS}" -o bin/tx-relay ./cmd/tx-relay

# ---- 3. agent (host + linux + windows/amd64) -----------------------------------
if ls cmd/tx-agent/*.go >/dev/null 2>&1; then
  log "go build tx-agent (host $(go env GOOS)/$(go env GOARCH))"
  go build -trimpath -ldflags "${LDFLAGS}" -o bin/tx-agent ./cmd/tx-agent
  for arch in amd64 arm64; do
    log "go build tx-agent-linux-${arch}"
    GOOS=linux GOARCH="${arch}" go build -trimpath -ldflags "${LDFLAGS}" \
      -o "bin/tx-agent-linux-${arch}" ./cmd/tx-agent
  done
  log "go build tx-agent.exe (windows/amd64)"
  # AGENT_WINDOWS_LDFLAGS="-H=windowsgui" hides the console window of the
  # supervisor build; leave it unset for a debuggable console build.
  GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "${LDFLAGS} ${AGENT_WINDOWS_LDFLAGS:-}" -o bin/tx-agent.exe ./cmd/tx-agent
else
  warn "cmd/tx-agent has no Go sources yet; skipping the agent builds"
fi

log "done:"
ls -l bin/
