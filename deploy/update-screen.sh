#!/usr/bin/env bash
# Build the checked-out revision and restart the gateway in a detached screen session.
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCREEN_NAME="${JARVIS_SCREEN_NAME:-jarvis-gateway}"
BUILD_DIR="${JARVIS_BUILD_DIR:-${ROOT_DIR}/build}"
BINARY="${JARVIS_BINARY:-${BUILD_DIR}/gateway}"
LOG_FILE="${JARVIS_LOG_FILE:-${ROOT_DIR}/data/gateway.screen.log}"
ENV_FILE="${JARVIS_ENV_FILE:-${ROOT_DIR}/data/gateway.env}"
HEALTH_URL="${JARVIS_HEALTH_URL:-http://127.0.0.1:8080/healthz}"
SYSTEMD_UNIT="${JARVIS_SYSTEMD_UNIT:-jarvis-gateway.service}"
MIN_FREE_KB="${JARVIS_MIN_FREE_KB:-524288}"

if [[ ! "${SCREEN_NAME}" =~ ^[A-Za-z0-9_.-]+$ ]]; then
  echo "invalid screen session name: ${SCREEN_NAME}" >&2
  exit 2
fi

for command_name in go npm screen curl; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "required command not found: ${command_name}" >&2
    exit 2
  fi
done

if [[ -n "${JARVIS_CONFIG_FILE:-}" ]]; then
  CONFIG_FILE="${JARVIS_CONFIG_FILE}"
elif [[ -f "${ROOT_DIR}/etc/gateway.server.yaml" ]]; then
  CONFIG_FILE="${ROOT_DIR}/etc/gateway.server.yaml"
else
  CONFIG_FILE="${ROOT_DIR}/etc/gateway.yaml"
fi
if [[ ! -f "${CONFIG_FILE}" ]]; then
  echo "gateway config not found: ${CONFIG_FILE}" >&2
  exit 2
fi

available_kb="$(df -Pk "${ROOT_DIR}" | awk 'NR == 2 {print $4}')"
if [[ ! "${available_kb}" =~ ^[0-9]+$ ]] || (( available_kb < MIN_FREE_KB )); then
  echo "insufficient free disk space: ${available_kb:-unknown} KiB available, ${MIN_FREE_KB} KiB required" >&2
  exit 1
fi

mkdir -p "${BUILD_DIR}" "$(dirname "${LOG_FILE}")"

screen_session_running() {
  screen -ls 2>/dev/null | awk -v name="${SCREEN_NAME}" '
    {
      session = $1
      sub(/^[0-9]+\./, "", session)
      if (session == name) found = 1
    }
    END { exit !found }
  '
}

echo "==> Installing and building web"
(
  cd "${ROOT_DIR}/web"
  npm ci
  npm run build
)

echo "==> Building gateway"
(
  cd "${ROOT_DIR}"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "${BINARY}.next" ./cmd/gateway
)
chmod 0755 "${BINARY}.next"
mv -f "${BINARY}.next" "${BINARY}"

if [[ -f "${ENV_FILE}" ]]; then
  echo "==> Loading environment from ${ENV_FILE}"
  set -a
  # shellcheck disable=SC1090
  source "${ENV_FILE}"
  set +a
fi

if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "${SYSTEMD_UNIT}"; then
  echo "==> Stopping ${SYSTEMD_UNIT} to release the gateway port"
  systemctl stop "${SYSTEMD_UNIT}"
fi

if screen_session_running; then
  echo "==> Stopping screen session ${SCREEN_NAME}"
  screen -S "${SCREEN_NAME}" -X quit
  for _ in {1..30}; do
    if ! screen_session_running; then
      break
    fi
    sleep 1
  done
fi

echo "==> Starting gateway in screen session ${SCREEN_NAME}"
cd "${ROOT_DIR}"
screen -L -Logfile "${LOG_FILE}" -dmS "${SCREEN_NAME}" "${BINARY}" -f "${CONFIG_FILE}"

for _ in {1..30}; do
  if ! screen_session_running; then
    break
  fi
  if curl --fail --silent "${HEALTH_URL}" >/dev/null; then
    echo "==> Deployment complete"
    echo "    screen: ${SCREEN_NAME}"
    echo "    binary: ${BINARY}"
    echo "    web:    ${ROOT_DIR}/web/dist"
    echo "    log:    ${LOG_FILE}"
    exit 0
  fi
  sleep 1
done

echo "gateway health check failed: ${HEALTH_URL}" >&2
echo "last log lines:" >&2
tail -n 50 "${LOG_FILE}" >&2 || true
exit 1
