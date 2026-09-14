#!/usr/bin/env bash
# Create a consistent, portable JarvisServer data backup without stopping Gateway.
# The archive intentionally contains runtime data, not source/build artifacts.
set -Eeuo pipefail

INSTALL_DIR="${JARVIS_INSTALL_DIR:-/root/JarvisServer}"
DATABASE_PATH="${JARVIS_DATABASE_PATH:-${INSTALL_DIR}/data/gateway.db}"
DOCUMENTS_DIR="${JARVIS_DOCUMENTS_DIR:-${INSTALL_DIR}/data/documents}"
WORKSPACES_DIR="${JARVIS_WORKSPACES_DIR:-${INSTALL_DIR}/workspaces}"
RUNTIME_DIR="${JARVIS_RUNTIME_DIR:-${INSTALL_DIR}/runtime}"
HOME_DIR="${JARVIS_HOME_DIR:-${INSTALL_DIR}/data/home}"
SKILLS_DIR="${JARVIS_SKILLS_DIR:-${INSTALL_DIR}/data/skills}"
CONFIG_FILE="${JARVIS_CONFIG_FILE:-${INSTALL_DIR}/etc/gateway.server.yaml}"
ENV_FILE="${JARVIS_ENV_FILE:-${INSTALL_DIR}/data/gateway.env}"
BACKUP_DIR="${JARVIS_BACKUP_DIR:-${INSTALL_DIR}/backups/data}"
RETENTION_DAYS="${JARVIS_BACKUP_RETENTION_DAYS:-14}"
RETENTION_COUNT="${JARVIS_BACKUP_RETENTION_COUNT:-7}"
MIN_FREE_KB="${JARVIS_BACKUP_MIN_FREE_KB:-524288}"
DRY_RUN=false

usage() {
  cat <<'EOF'
Usage: bash deploy/backup-data.sh [--dry-run]

Creates a gzip-compressed archive containing:
  - a transactionally consistent SQLite copy;
  - uploaded project documents;
  - coding workspaces (including Git metadata);
  - runtime/JARVIS_HOME data, memory, skills and plugins when present;
  - production config and environment files (the archive contains secrets).

Environment overrides:
  JARVIS_INSTALL_DIR               Installation root
  JARVIS_DATABASE_PATH             SQLite database path
  JARVIS_DOCUMENTS_DIR             Project document storage
  JARVIS_WORKSPACES_DIR            Code workspace storage
  JARVIS_RUNTIME_DIR               Gateway runtime/Cwd data
  JARVIS_HOME_DIR                  JARVIS_HOME data
  JARVIS_SKILLS_DIR                Skill storage
  JARVIS_CONFIG_FILE               Production YAML config
  JARVIS_ENV_FILE                  Service environment/secrets file
  JARVIS_BACKUP_DIR                Backup destination
  JARVIS_BACKUP_RETENTION_DAYS     Delete archives older than N days (default 14)
  JARVIS_BACKUP_RETENTION_COUNT    Always retain newest N archives (default 7)
  JARVIS_BACKUP_MIN_FREE_KB        Required destination free space (default 512 MiB)
EOF
}

while (($#)); do
  case "$1" in
    --dry-run) DRY_RUN=true ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

fail() { printf 'error: %s\n' "$*" >&2; exit 1; }
log() { printf '==> %s\n' "$*"; }

for value in RETENTION_DAYS RETENTION_COUNT MIN_FREE_KB; do
  [[ "${!value}" =~ ^[0-9]+$ ]] || fail "${value} must be a non-negative integer"
done
for command_name in python3 sha256sum flock; do
  command -v "${command_name}" >/dev/null 2>&1 || fail "${command_name} is required"
done
[[ -f "${DATABASE_PATH}" ]] || fail "database not found: ${DATABASE_PATH}"

items=(
  "documents=${DOCUMENTS_DIR}"
  "workspaces=${WORKSPACES_DIR}"
  "runtime=${RUNTIME_DIR}"
  "jarvis-home=${HOME_DIR}"
  "skills=${SKILLS_DIR}"
  "config/gateway.yaml=${CONFIG_FILE}"
  "config/gateway.env=${ENV_FILE}"
)

log "backup plan"
echo "    database: ${DATABASE_PATH}"
for item in "${items[@]}"; do
  label="${item%%=*}"
  path="${item#*=}"
  if [[ -e "${path}" ]]; then
    echo "    ${label}: ${path}"
  else
    echo "    ${label}: ${path} (not present, skipped)"
  fi
done
echo "    destination: ${BACKUP_DIR}"
echo "    retention: ${RETENTION_DAYS} days, always keep newest ${RETENTION_COUNT}"
${DRY_RUN} && exit 0

mkdir -p "${BACKUP_DIR}"
chmod 0700 "${BACKUP_DIR}"
available_kb="$(df -Pk "${BACKUP_DIR}" | awk 'NR == 2 {print $4}')"
[[ "${available_kb}" =~ ^[0-9]+$ ]] || fail "could not determine destination free space"
(( available_kb >= MIN_FREE_KB )) ||
  fail "insufficient backup space: ${available_kb} KiB available, ${MIN_FREE_KB} KiB required"

LOCK_FILE="${JARVIS_BACKUP_LOCK:-${BACKUP_DIR}/.backup.lock}"
exec 9>"${LOCK_FILE}"
flock -n 9 || fail "another backup is already running"

stamp="$(date -u +%Y%m%dT%H%M%SZ)"
host="$(hostname -s 2>/dev/null || hostname)"
base="jarvis-data-${host}-${stamp}"
archive="${BACKUP_DIR}/${base}.tar.gz"
partial="${archive}.partial"
checksum="${archive}.sha256"
temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/jarvis-backup.XXXXXX")"
trap 'rm -rf "${temp_dir}" "${partial}"' EXIT
chmod 0700 "${temp_dir}"

log "creating consistent SQLite backup"
python3 - "${DATABASE_PATH}" "${temp_dir}/gateway.db" <<'PY'
import sqlite3, sys
source, target = sys.argv[1:3]
src = sqlite3.connect(f"file:{source}?mode=ro", uri=True, timeout=60)
dst = sqlite3.connect(target)
with dst:
    src.backup(dst, pages=1024, sleep=0.05)
result = dst.execute("PRAGMA integrity_check").fetchone()[0]
dst.close(); src.close()
if result != "ok":
    raise SystemExit(f"backup integrity check failed: {result}")
PY
chmod 0600 "${temp_dir}/gateway.db"

manifest="${temp_dir}/manifest.json"
python3 - "${manifest}" "${stamp}" "${host}" "${DATABASE_PATH}" "${items[@]}" <<'PY'
import json, os, sys
out, stamp, host, database, *items = sys.argv[1:]
entries = [{"archive_path": "database/gateway.db", "source": database, "present": True}]
for item in items:
    label, path = item.split("=", 1)
    entries.append({"archive_path": label, "source": path, "present": os.path.exists(path)})
manifest = {
    "format": "jarvis-data-backup-v1",
    "created_at": stamp,
    "host": host,
    "contains_secrets": True,
    "entries": entries,
}
with open(out, "w", encoding="utf-8") as f:
    json.dump(manifest, f, ensure_ascii=False, indent=2)
PY
chmod 0600 "${manifest}"

log "packing data archive (may take time for large workspaces)"
python3 - "${partial}" "${temp_dir}/gateway.db" "${manifest}" "${items[@]}" <<'PY'
import os, sys, tarfile
archive, database, manifest, *items = sys.argv[1:]
with tarfile.open(archive, "w:gz", compresslevel=6) as tar:
    tar.add(manifest, arcname="manifest.json", recursive=False)
    tar.add(database, arcname="database/gateway.db", recursive=False)
    seen = set()
    for item in items:
        label, path = item.split("=", 1)
        if not os.path.exists(path):
            continue
        real = os.path.realpath(path)
        # Avoid duplicate data when JARVIS_HOME or skills are nested in runtime.
        if real in seen:
            continue
        seen.add(real)
        tar.add(path, arcname=label, recursive=True)
PY
chmod 0600 "${partial}"
mv "${partial}" "${archive}"
sha256sum "${archive}" >"${checksum}"
chmod 0600 "${checksum}"

log "applying retention policy"
python3 - "${BACKUP_DIR}" "${RETENTION_DAYS}" "${RETENTION_COUNT}" <<'PY'
import glob, os, sys, time
root, days, keep = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
files = sorted(glob.glob(os.path.join(root, "jarvis-data-*.tar.gz")), key=os.path.getmtime, reverse=True)
cutoff = time.time() - days * 86400
for index, path in enumerate(files):
    if index < keep or (days > 0 and os.path.getmtime(path) >= cutoff):
        continue
    os.remove(path)
    sidecar = path + ".sha256"
    if os.path.exists(sidecar):
        os.remove(sidecar)
PY

log "backup complete"
echo "    archive: ${archive}"
echo "    checksum: ${checksum}"
echo "    size: $(du -h "${archive}" | awk '{print $1}')"
echo "    WARNING: archive contains credentials; store and transfer it securely."
