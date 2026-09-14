# History window b0a8b28 deployment report — 2026-09-11

## Result

**PASS** for code implementation, tests, ordinary push, isolated staging build, atomic production switch, service health, public static delivery, and non-sensitive public config. Authenticated session endpoint regression is **BLOCKED**: no credential/token/cookie/chat body was read or supplied, so no `/v1/agent/sessions/:id` response is claimed.

## Source and repository

- Baseline deployed before this change: `88859c989dac42647d5d3c2edf79c31c62f31843`.
- Implemented commit: `b0a8b2886837a1b900c9b4cce42f72a4fbdf38e2`.
- Branch: `feat/tasks`; ordinary `git push origin feat/tasks` succeeded; remote resolved to the same commit.
- Existing local/server dirty and untracked content was not cleaned, pulled over, stashed, reset, rebased, or overwritten.
- Existing `data`, runtime-related content, releases, and `web/dist.previous` were preserved.

## Actual implementation

- `GET /v1/agent/sessions/:id` now defaults to `limit=30`; explicit limits remain supported and clamp at 200.
- `before_seq` remains exclusive and returns ascending order; `after_seq` remains exclusive and returns ascending order. The page cursor is derived from the retained page boundary, with an explicit regression test for default/bounds/precedence.
- Added `around_seq`: returns the target entry plus up to 15 preceding and 30 following persisted entries, ordered by database sequence.
- Added safe `seq` metadata to restored messages for stable window positioning; stable persisted entry `id` remains the frontend deduplication key.
- Added frontend restore option `aroundSeq`, request parameter support, sequence sorting in the Sessions detail viewer, and tested `mergeRestoredMessages` deduplication/sorting helper. Existing POST chat context loading remains on full `LoadEntries` and was not changed.
- Account ownership check remains after paged/around load and the SQL/session lookup does not bypass tenant enforcement.
- Tool-result entries remain internal to assistant tool steps and are counted as persisted entries for window bounds; only user/assistant restored messages are exposed, preserving existing display semantics.

## Verification

### Local

- Go: `go test ./...` PASS.
- Web: `npm test -- --run` PASS: 17 files, 63 tests.
- Web: `npm run build` PASS (`tsc` included). Existing Vite warning remains for a >500 kB chunk.
- `git diff --check` PASS.
- No database schema, migration, ANALYZE, VACUUM, load test, or real model call performed.

### Production staging

- Independent staging: `/root/JarvisServer/releases/history-window-b0a8b28-20260911T032501Z`.
- Staging Go full tests: PASS.
- Staging web `npm ci --ignore-scripts`: completed; `npm test -- --run`: PASS, 17 files / 63 tests.
- Staging `npm run build`: PASS. Existing audit report: 4 moderate and 2 high dependency findings; no audit fix was run.
- Staging artifacts:
  - gateway SHA256 `546895c5cdef4c329d753468f85b5cc9d7fdce916d3f7ea97203280dbf71ac8e`
  - index SHA256 `ce8cf36c28eda67fa9579fad4e583b67a81ed20f101e62dad69a56b42ba14ee5`

### Production switch

- Backup: `/root/JarvisServer/backups/history-window-b0a8b28-20260911T033008Z`.
- Backed up the current running binary, current index, and current index-referenced assets; manifest retained in backup.
- Installed new binary and new hashed assets via sibling temporary files and atomic rename; old chunks and dirty content were retained.
- Atomically replaced `web/dist/index.html`.
- Only `systemctl restart jarvis-gateway` was executed. OpenClaw and Caddy were not restarted.
- First immediate health probe raced startup and failed to connect; subsequent five probes returned HTTP 200 `ok`. This is recorded as startup readiness delay, not a failed deployment.

### Live evidence

- `jarvis-gateway`: `active/running`, `MainPID=720797`, `NRestarts=0`.
- `GET http://127.0.0.1:20128/healthz`: HTTP 200, `ok`.
- `GET http://127.0.0.1:8080/healthz`: HTTP 200, `ok` during preflight.
- Live binary SHA256 equals staging: `546895c5cdef4c329d753468f85b5cc9d7fdce916d3f7ea97203280dbf71ac8e`.
- Live index SHA256 equals staging: `ce8cf36c28eda67fa9579fad4e583b67a81ed20f101e62dad69a56b42ba14ee5`.
- Public `/` and `/login` returned the same live index hash.
- Public config endpoint returned `{"registration_enabled":false}`; no secret/token/cookie was read.
- Existing `/root/JarvisServer/data`, `/root/JarvisServer/releases`, and `/root/JarvisServer/web/dist.previous` were confirmed present after deployment.
- Service journal showed clean stop/start and ready at `03:30:14 UTC`; no restart loop.

## Blocked / not claimed

- Authenticated `GET /v1/agent/sessions/:id` default-30, explicit-limit, before/after, around window, ownership, tool boundary, and post-chat-context behavior were not executed online because doing so would require a valid authenticated session and would expose session metadata/body. Local unit/HTTP coverage is the evidence for these contracts.
- No browser authenticated UI regression was performed.
- No rollback was needed; rollback materials remain in the backup directory. No database restore is part of rollback.
- `sqlite3` CLI was unavailable on the server during the final non-mutating run-count probe; no run count was fabricated. Service was active and `NRestarts=0`.
