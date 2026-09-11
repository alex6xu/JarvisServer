package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const projectTipRunsSchema = `
CREATE TABLE project_tip_runs (
 id TEXT PRIMARY KEY,
 account_id INTEGER NOT NULL,
 project_id TEXT NOT NULL,
 tip_id TEXT NOT NULL REFERENCES project_tips(id) ON DELETE CASCADE,
 idempotency_key TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN ('analyze','execute')),
 session_mode TEXT NOT NULL,
 workspace_id TEXT NOT NULL DEFAULT '',
 snapshot TEXT NOT NULL,
 session_id TEXT NOT NULL DEFAULT '',
 run_id TEXT NOT NULL DEFAULT '',
 launch_status TEXT NOT NULL CHECK(launch_status IN ('starting','launched','failed')),
 error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 UNIQUE(account_id,tip_id,idempotency_key)
);
CREATE UNIQUE INDEX idx_tip_run_starting ON project_tip_runs(tip_id) WHERE launch_status='starting';
CREATE INDEX idx_tip_runs_history ON project_tip_runs(account_id,project_id,tip_id,created_at);
`

type ProjectTipRun struct {
	ID          string     `json:"id"`
	Mode        string     `json:"mode"`
	SessionMode string     `json:"session_mode"`
	WorkspaceID string     `json:"workspace_id,omitempty"`
	Snapshot    ProjectTip `json:"snapshot"`
	SessionID   string     `json:"session_id,omitempty"`
	RunID       string     `json:"run_id,omitempty"`
	Status      string     `json:"status"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   string     `json:"created_at"`
}

func (s *GatewayStore) ListProjectTipRuns(ctx context.Context, accountID int, projectID, tipID string) ([]ProjectTipRun, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.mode,t.session_mode,t.workspace_id,t.snapshot,t.session_id,t.run_id,CASE WHEN t.launch_status='launched' THEN COALESCE(r.status,'unavailable') ELSE t.launch_status END,CASE WHEN t.error<>'' THEN t.error ELSE COALESCE(r.error,'') END,t.created_at FROM project_tip_runs t LEFT JOIN runs r ON r.id=t.run_id WHERE t.account_id=? AND t.project_id=? AND t.tip_id=? ORDER BY t.created_at DESC`, accountID, projectID, tipID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProjectTipRun, 0)
	for rows.Next() {
		var item ProjectTipRun
		var snapshot string
		if err := rows.Scan(&item.ID, &item.Mode, &item.SessionMode, &item.WorkspaceID, &snapshot, &item.SessionID, &item.RunID, &item.Status, &item.Error, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(snapshot), &item.Snapshot); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

var errTipRunning = errors.New("tip already has an active execution")

// Reserve under a database transaction: duplicate requests never create two agents.
// Completed/failed idempotency keys remain durable and may not be reused.
func (s *GatewayStore) reserveTipRun(ctx context.Context, tip ProjectTip, mode, sessionMode, workspaceID, key string) (string, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	// Acquire the SQLite write lock before reading the active/idempotency state.
	if _, err = tx.ExecContext(ctx, `UPDATE project_tips SET id=id WHERE id=? AND account_id=? AND project_id=?`, tip.ID, tip.AccountID, tip.ProjectID); err != nil {
		return "", false, err
	}
	var id, oldMode string
	err = tx.QueryRowContext(ctx, `SELECT id,mode FROM project_tip_runs WHERE account_id=? AND tip_id=? AND idempotency_key=?`, tip.AccountID, tip.ID, key).Scan(&id, &oldMode)
	if err == nil {
		if oldMode != mode {
			return "", false, errors.New("idempotency key belongs to another mode")
		}
		return id, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	var active int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_tip_runs t LEFT JOIN runs r ON r.id=t.run_id WHERE t.tip_id=? AND (t.launch_status='starting' OR (t.launch_status='launched' AND r.status='running'))`, tip.ID).Scan(&active); err != nil {
		return "", false, err
	}
	if active > 0 {
		return "", false, errTipRunning
	}
	// Read the snapshot inside the same transaction so it is exactly the reserved revision.
	tip, err = scanProjectTip(tx.QueryRowContext(ctx, `SELECT `+projectTipColumns+` FROM project_tips WHERE id=? AND account_id=? AND project_id=?`, tip.ID, tip.AccountID, tip.ProjectID))
	if err != nil {
		return "", false, err
	}
	snapshot, err := json.Marshal(tip)
	if err != nil {
		return "", false, err
	}
	id = newID("tiprun")
	_, err = tx.ExecContext(ctx, `INSERT INTO project_tip_runs(id,account_id,project_id,tip_id,idempotency_key,mode,session_mode,workspace_id,snapshot,launch_status,created_at) VALUES(?,?,?,?,?,?,?,?,?,'starting',?)`, id, tip.AccountID, tip.ProjectID, tip.ID, key, mode, sessionMode, workspaceID, string(snapshot), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return "", false, err
	}
	return id, false, tx.Commit()
}

func (s *GatewayStore) launchTipRun(ctx context.Context, id string, tip ProjectTip, response ChatResponse) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE project_tip_runs SET session_id=?,run_id=?,launch_status='launched' WHERE id=? AND launch_status='starting'`, response.SessionID, response.RunID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return errors.New("tip launch reservation lost")
	}
	_, err = tx.ExecContext(ctx, `UPDATE project_tips SET status='doing',completed_at='',archived_at='',version=version+1,updated_at=? WHERE id=? AND account_id=? AND project_id=?`, time.Now().UTC().Format(time.RFC3339Nano), tip.ID, tip.AccountID, tip.ProjectID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) handleExecuteProjectTip(w http.ResponseWriter, r *http.Request) {
	accountID, ok := s.requestAccountID(r)
	if !ok {
		writeErr(w, 401, "account context is required")
		return
	}
	projectID, tipID := pathParam(r, "projectId"), pathParam(r, "tipId")
	project, err := s.Audit.ProjectByID(r.Context(), accountID, projectID)
	if err != nil || project.Status != "active" {
		writeErr(w, 404, "project not found")
		return
	}
	tip, err := s.Audit.ProjectTipByID(r.Context(), accountID, projectID, tipID)
	if err != nil {
		writeErr(w, 404, "tip not found")
		return
	}
	var body struct {
		Mode           string `json:"mode"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxTipRequestBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || (body.Mode != "analyze" && body.Mode != "execute") {
		writeErr(w, 400, "mode must be analyze or execute")
		return
	}
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.IdempotencyKey == "" || len(body.IdempotencyKey) > 128 {
		writeErr(w, 400, "idempotency_key is required (maximum 128 bytes)")
		return
	}
	sessionMode := "chat"
	workspaceID := project.LinkedWorkspaceID
	if workspaceID != "" {
		if _, err := s.workspaceInfoForAccount(workspaceID, accountID); err != nil {
			writeErr(w, 404, "workspace not found")
			return
		}
		sessionMode = "coder"
	}
	id, replay, err := s.Audit.reserveTipRun(r.Context(), tip, body.Mode, sessionMode, workspaceID, body.IdempotencyKey)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	// From reservation onward use a detached context: client disconnects must not orphan launches.
	ctx := context.Background()
	history, err := s.Audit.ListProjectTipRuns(ctx, accountID, projectID, tipID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var reserved ProjectTipRun
	for _, item := range history {
		if item.ID == id {
			reserved = item
			break
		}
	}
	if !replay {
		instruction := "Execute the following project Tip. Report what you changed and any verification. Do not mark the Tip done; completion is a user decision."
		if body.Mode == "analyze" {
			instruction = "Analyze the following project Tip and propose a plan. This is read-only analysis: no file mutations, commands, or external actions. Tools are disabled."
		} else if workspaceID == "" {
			instruction = "Help resolve the following project Tip in chat. No workspace is bound; do not claim to have modified project files. Completion is a user decision."
		}
		snapshot, _ := json.Marshal(reserved.Snapshot)
		_, err = s.StartChat(ctx, ChatRequest{AccountID: accountID, ProjectID: projectID, WorkspaceID: workspaceID, Mode: sessionMode, ReadOnly: body.Mode == "analyze", Message: instruction + "\n\nTip snapshot:\n" + string(snapshot), BeforeLaunch: func(response ChatResponse) error { return s.Audit.launchTipRun(ctx, id, tip, response) }})
		if err != nil {
			_, persistErr := s.Audit.db.ExecContext(ctx, `UPDATE project_tip_runs SET launch_status='failed',error=? WHERE id=? AND launch_status='starting'`, err.Error(), id)
			if persistErr != nil {
				writeErr(w, 500, persistErr.Error())
				return
			}
		}
	}
	history, loadErr := s.Audit.ListProjectTipRuns(ctx, accountID, projectID, tipID)
	if loadErr != nil {
		writeErr(w, 500, loadErr.Error())
		return
	}
	for _, item := range history {
		if item.ID == id {
			reserved = item
			break
		}
	}
	updated, loadErr := s.Audit.ProjectTipByID(ctx, accountID, projectID, tipID)
	if loadErr != nil {
		writeErr(w, 500, loadErr.Error())
		return
	}
	updated.Runs = history
	status := http.StatusAccepted
	if reserved.Status == "failed" {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{"tip": updated, "execution": reserved, "replayed": replay})
}
