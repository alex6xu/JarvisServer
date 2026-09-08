#!/usr/bin/env bash
# Safely sync this checkout to a JarvisServer installation, build production
# artifacts, restart systemd, and roll back the artifacts if health checks fail.
set -Eeuo pipefail

SOURCE_DIR="${JARVIS_SOURCE_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
INSTALL_DIR="${JARVIS_INSTALL_DIR:-/root/JarvisServer}"
SYSTEMD_UNIT="${JARVIS_SYSTEMD_UNIT:-jarvis-gateway.service}"
HEALTH_URL="${JARVIS_HEALTH_URL:-http://127.0.0.1:8080/healthz}"
CONFIG_FILE="${JARVIS_CONFIG_FILE:-${INSTALL_DIR}/etc/gateway.server.yaml}"
MIN_FREE_KB="${JARVIS_MIN_FREE_KB:-1048576}"
HEALTH_ATTEMPTS="${JARVIS_HEALTH_ATTEMPTS:-45}"
GO_BIN="${JARVIS_GO:-}"
DRY_RUN=false
SKIP_TESTS=false
NO_RESTART=false

usage() {
  cat <<'EOF'
Usage: bash deploy/sync-install.sh [options]

Options:
  --dry-run       Show what would happen; do not build, copy, or restart.
  --skip-tests    Skip Go and Web tests (production builds still run).
  --no-restart    Sync/build artifacts but do not restart the service.
  -h, --help      Show this help.

Environment overrides:
  JARVIS_SOURCE_DIR       Source checkout (default: repository containing script)
  JARVIS_INSTALL_DIR      Installation root (default: /root/JarvisServer)
  JARVIS_CONFIG_FILE      Production config required before restart
  JARVIS_SYSTEMD_UNIT     systemd unit (default: jarvis-gateway.service)
  JARVIS_HEALTH_URL       Health endpoint (default: http://127.0.0.1:8080/healthz)
  JARVIS_GO               Native Go executable; Snap Go is rejected
  JARVIS_MIN_FREE_KB      Required free disk space (default: 1 GiB)
  JARVIS_HEALTH_ATTEMPTS  One-second health-check attempts (default: 45)
EOF
}

while (($#)); do
  case "$1" in
    --dry-run) DRY_RUN=true ;;
    --skip-tests) SKIP_TESTS=true ;;
    --no-restart) NO_RESTART=true ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

SOURCE_DIR="$(realpath -m "${SOURCE_DIR}")"
INSTALL_DIR="$(realpath -m "${INSTALL_DIR}")"

log() { printf '==> %s\n' "$*"; }
fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
run() {
  if ${DRY_RUN}; then
    printf '+ '
    printf '%q ' "$@"
    printf '\n'
  else
    "$@"
  fi
}

[[ -f "${SOURCE_DIR}/go.mod" && -f "${SOURCE_DIR}/web/package.json" ]] ||
  fail "source does not look like JarvisServer: ${SOURCE_DIR}"
[[ "${SOURCE_DIR}" != "/" && "${INSTALL_DIR}" != "/" ]] || fail "refusing to use filesystem root"
[[ "${MIN_FREE_KB}" =~ ^[0-9]+$ ]] || fail "JARVIS_MIN_FREE_KB must be an integer"
[[ "${HEALTH_ATTEMPTS}" =~ ^[0-9]+$ ]] || fail "JARVIS_HEALTH_ATTEMPTS must be an integer"

for command_name in realpath npm curl flock; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "required command not found: ${command_name}"
done
if [[ "${SOURCE_DIR}" != "${INSTALL_DIR}" ]]; then
  command -v rsync >/dev/null 2>&1 || fail "rsync is required when source and installation directories differ"
fi

resolve_go() {
  local candidate resolved
  if [[ -n "${GO_BIN}" ]]; then
    candidate="${GO_BIN}"
  else
    candidate="$(command -v go 2>/dev/null || true)"
    resolved="$(readlink -f "${candidate}" 2>/dev/null || true)"
    if [[ -z "${candidate}" || "${candidate}" == /snap/* || "${resolved}" == */snap ]]; then
      candidate="$(find /root/go/pkg/mod/golang.org/toolchain@*/bin -maxdepth 1 -type f -name go -perm -111 2>/dev/null | sort -V | tail -n 1)"
    fi
  fi
  [[ -n "${candidate}" && -x "${candidate}" ]] ||
    fail "native Go toolchain not found; set JARVIS_GO (Snap Go is unsupported for hardened services)"
  resolved="$(readlink -f "${candidate}")"
  [[ "${candidate}" != /snap/* && "${resolved}" != */snap ]] ||
    fail "Snap Go is unsupported; set JARVIS_GO to a native Go executable"
  GO_BIN="${candidate}"
}
resolve_go

available_kb="$(df -Pk "$(dirname "${INSTALL_DIR}")" | awk 'NR == 2 {print $4}')"
[[ "${available_kb}" =~ ^[0-9]+$ ]] || fail "could not determine free disk space"
(( available_kb >= MIN_FREE_KB )) ||
  fail "insufficient disk space: ${available_kb} KiB available, ${MIN_FREE_KB} KiB required"

if [[ "${SOURCE_DIR}" != "${INSTALL_DIR}" ]]; then
  case "${INSTALL_DIR}/" in
    "${SOURCE_DIR}/"*) fail "installation directory must not be inside source directory" ;;
  esac
fi

LOCK_FILE="${JARVIS_SYNC_LOCK:-/tmp/jarvis-sync-install.lock}"
exec 9>"${LOCK_FILE}"
flock -n 9 || fail "another sync-install process is already running"

if ${DRY_RUN}; then
  STAGE_DIR="${TMPDIR:-/tmp}/jarvis-sync-install.dry-run"
else
  # Keep staging outside both source and installation trees. The common layout
  # stores workspaces below /root/JarvisServer; staging under the source could be
  # removed by rsync --delete before artifact installation.
  STAGE_DIR="$(mktemp -d "${TMPDIR:-/tmp}/jarvis-sync-install.XXXXXX")"
fi
STAGE_GATEWAY="${STAGE_DIR}/gateway"
STAGE_WEB="${STAGE_DIR}/web"
BACKUP_DIR="${INSTALL_DIR}/backups/sync-install-$(date -u +%Y%m%dT%H%M%SZ)"

if ${DRY_RUN}; then
  log "dry run"
  echo "    source:  ${SOURCE_DIR}"
  echo "    install: ${INSTALL_DIR}"
  echo "    go:      ${GO_BIN}"
  echo "    config:  ${CONFIG_FILE}"
else
  trap 'rm -rf "${STAGE_DIR}"' EXIT
  mkdir -p "${STAGE_WEB}"
fi

if ! ${DRY_RUN}; then
  log "checking native Go toolchain"
  "${GO_BIN}" version
fi

if ! ${SKIP_TESTS}; then
  log "running Go tests"
  if ${DRY_RUN}; then
    run bash -lc "cd $(printf %q "${SOURCE_DIR}") && GOTOOLCHAIN=local $(printf %q "${GO_BIN}") test ./internal/gateway ./internal/agenttool -count=1"
  else
    (cd "${SOURCE_DIR}" && env GOTOOLCHAIN=local "${GO_BIN}" test ./internal/gateway ./internal/agenttool -count=1)
  fi
  log "running Web tests"
  if ${DRY_RUN}; then
    run bash -lc "cd $(printf %q "${SOURCE_DIR}/web") && npm test"
  else
    (cd "${SOURCE_DIR}/web" && npm test)
  fi
fi

log "building Gateway"
if ${DRY_RUN}; then
  run env GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64 "${GO_BIN}" build -trimpath -ldflags=-s\ -w -o "${STAGE_GATEWAY}" ./cmd/gateway
else
  (cd "${SOURCE_DIR}" && env GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    "${GO_BIN}" build -trimpath -ldflags='-s -w' -o "${STAGE_GATEWAY}" ./cmd/gateway)
  chmod 0755 "${STAGE_GATEWAY}"
fi

log "building Web"
if ${DRY_RUN}; then
  run bash -lc "cd $(printf %q "${SOURCE_DIR}/web") && npm ci && npm run build"
else
  (cd "${SOURCE_DIR}/web" && npm ci && npm run build)
  cp -a "${SOURCE_DIR}/web/dist/." "${STAGE_WEB}/"
fi

if [[ "${SOURCE_DIR}" != "${INSTALL_DIR}" ]]; then
  log "syncing source tree to ${INSTALL_DIR}"
  # --delete removes stale source files but every runtime/deployment path below is
  # excluded and therefore protected from deletion. Never copy .git between two
  # checkouts: doing so caused the previous mixed-commit/rebase corruption.
  RSYNC_ARGS=(
    -a --delete --itemize-changes
    --exclude=/.git/
    --exclude=/.workspace.json
    --exclude=/.artifacts/
    --exclude=/build/
    --exclude=/data/
    --exclude=/runtime/
    --exclude=/workspaces/
    --exclude=/backups/
    --exclude=/releases/
    --exclude=/jarvisserver
    --exclude=/etc/gateway.server.yaml
    --exclude=/etc/gateway.yaml.*
    --exclude=/web/node_modules/
    --exclude=/web/dist/
  )
  ${DRY_RUN} && RSYNC_ARGS+=(--dry-run)
  run mkdir -p "${INSTALL_DIR}"
  run rsync "${RSYNC_ARGS[@]}" "${SOURCE_DIR}/" "${INSTALL_DIR}/"
else
  log "source is installation directory; skipping source-tree copy"
fi

if ${DRY_RUN}; then
  log "would back up and atomically install Gateway + Web artifacts"
else
  log "backing up current artifacts to ${BACKUP_DIR}"
  mkdir -p "${BACKUP_DIR}"
  [[ -f "${INSTALL_DIR}/build/gateway" ]] && cp -a "${INSTALL_DIR}/build/gateway" "${BACKUP_DIR}/gateway"
  [[ -d "${INSTALL_DIR}/web/dist" ]] && cp -a "${INSTALL_DIR}/web/dist" "${BACKUP_DIR}/dist"

  log "installing artifacts atomically"
  mkdir -p "${INSTALL_DIR}/build" "${INSTALL_DIR}/web"
  install -m 0755 "${STAGE_GATEWAY}" "${INSTALL_DIR}/build/gateway.next"
  rm -rf "${INSTALL_DIR}/web/dist.next"
  mkdir -p "${INSTALL_DIR}/web/dist.next"
  cp -a "${STAGE_WEB}/." "${INSTALL_DIR}/web/dist.next/"
  mv -f "${INSTALL_DIR}/build/gateway.next" "${INSTALL_DIR}/build/gateway"
  rm -rf "${INSTALL_DIR}/web/dist.previous"
  [[ -d "${INSTALL_DIR}/web/dist" ]] && mv "${INSTALL_DIR}/web/dist" "${INSTALL_DIR}/web/dist.previous"
  mv "${INSTALL_DIR}/web/dist.next" "${INSTALL_DIR}/web/dist"
fi

if ${NO_RESTART}; then
  log "restart skipped (--no-restart)"
  exit 0
fi

[[ -f "${CONFIG_FILE}" ]] || fail "production config not found: ${CONFIG_FILE}"
command -v systemctl >/dev/null 2>&1 || fail "systemctl not found"

log "restarting ${SYSTEMD_UNIT}"
run systemctl restart "${SYSTEMD_UNIT}"
${DRY_RUN} && exit 0

for ((attempt=1; attempt<=HEALTH_ATTEMPTS; attempt++)); do
  if curl --fail --silent --max-time 3 "${HEALTH_URL}" >/dev/null; then
    log "deployment complete"
    echo "    commit:  $(git -C "${SOURCE_DIR}" rev-parse --short HEAD 2>/dev/null || echo working-tree)"
    echo "    binary:  ${INSTALL_DIR}/build/gateway"
    echo "    web:     ${INSTALL_DIR}/web/dist"
    echo "    backup:  ${BACKUP_DIR}"
    echo "    health:  ${HEALTH_URL}"
    exit 0
  fi
  sleep 1
done

log "health check failed; rolling back artifacts"
if [[ -f "${BACKUP_DIR}/gateway" ]]; then
  install -m 0755 "${BACKUP_DIR}/gateway" "${INSTALL_DIR}/build/gateway"
fi
if [[ -d "${BACKUP_DIR}/dist" ]]; then
  rm -rf "${INSTALL_DIR}/web/dist"
  cp -a "${BACKUP_DIR}/dist" "${INSTALL_DIR}/web/dist"
fi
systemctl restart "${SYSTEMD_UNIT}" || true
fail "deployment rolled back because health check failed: ${HEALTH_URL}"
