# Project Tips → Agent MVP

Implemented in the source workspace only. No installation synchronization, service restart, or production deployment is required to review/test this change.

## API

`POST /v1/projects/:projectId/tips/:tipId/execute`

```json
{"mode":"analyze","idempotency_key":"client-generated-unique-key"}
```

- `mode`: `analyze` or `execute`; `idempotency_key` is required, maximum 128 bytes.
- Account, project, Tip and linked workspace ownership are validated server-side. Clients cannot choose a different workspace, session, model, or project in the body.
- Workspace projects launch Code sessions in their bound workspace; other projects launch Chat sessions. Analysis in either session type disables all agent tools, plugins, delegation, and shell hooks, rather than relying on prompt instructions. The MVP analyzes the Tip snapshot, not workspace files.
- A new session is explicitly assigned to the project even when no documents are attached, using StartChat's existing initial-message project persistence.
- Returns HTTP 202 with `{tip, execution, replayed}` on launch/replay. `execution` includes session/run IDs, mode, workspace, current run status, and immutable Tip snapshot.
- A synchronous launch failure returns HTTP 502 with the durable failed execution and unchanged Tip status. History includes its error. A new idempotency key explicitly retries; the old key always replays the same attempt.
- Concurrent different-key requests while the Tip has a starting/running execution return HTTP 409. Same-key requests replay the existing reservation/attempt, including after completion.
- Only the pre-launch commit, after all StartChat setup succeeds and immediately before the agent goroutine starts, atomically links the run and marks the Tip `doing`. Agent completion does not mark it `done`.

Migration 25 adds `project_tip_runs` after the existing Tips migration 24. Reservations, snapshots, keys, launch errors and links are durable. Startup marks unfinished reservations failed; existing run recovery marks interrupted launched runs. This follows the gateway's existing single-service-process recovery model (not a multi-instance launch coordinator).

GET Tip, list Tips and PATCH Tip responses include `runs` history. Runtime status is read from the existing runs table; deleted/unavailable runs are shown as unavailable. Deleting a Tip cascades its association history, not its sessions.

## UI

Tips have explicit confirmed read-only analysis and Agent execution actions. History shows launch/run status, errors, the original Tip revision, and a session restore link. Restore uses the same account-scoped local storage and Chat/Code query parameters as project session navigation. A refresh action updates run history. Failed launches offer an explicit fresh-key retry; network failures retain their key to avoid an accidental duplicate.

The Tip panel is keyed by account/project. Async hook responses and project-detail requests are guarded against stale selections and unmounts.

## Validation

Go integration tests use a local fake OpenAI-compatible streaming provider, covering both modes with and without workspaces, project linkage without documents, successful completion without auto-completion of Tips, idempotent replay, active duplicate blocking, ownership, failed startup, concurrent reservations, and absence of analysis tools. Frontend unit tests cover session restore routes and active-run detection. Run `go test ./...`, `go test -race ./internal/gateway -run TestTipExecution`, and `cd web && npm run build && npm test`.
