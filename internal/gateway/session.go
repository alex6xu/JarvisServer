package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

// GetSession builds the SessionDetailResponse for web session restore.
func (s *Service) GetSession(id string) (SessionDetailResponse, error) {
	return s.getSessionForAccount(id, 0)
}

func (s *Service) getSessionForAccount(id string, accountID int) (SessionDetailResponse, error) {
	h, entries, err := s.Store.LoadEntries(id)
	if err != nil {
		return SessionDetailResponse{}, err
	}
	if !sessionOwnedByAccount(h, accountID) {
		return SessionDetailResponse{}, fmt.Errorf("session not found: %w", os.ErrNotExist)
	}
	msgs := entriesToRestored(entries, h.Model)
	meta := sessionMetaFromHeader(h, len(msgs))
	meta.Title = sessionTitle(msgs)
	meta.Preview = meta.Title
	resp := SessionDetailResponse{Session: meta, Messages: msgs, WorkspaceID: h.WorkspaceID}
	if active := s.Runs.ActiveForSession(id); active != nil {
		info := active.Info()
		resp.ActiveRun = &info
		resp.LastEventSeq = info.LastSeq
		resp.WorkspaceID = info.WorkspaceID
		resp.Session.WorkspaceID = info.WorkspaceID
		resp.Session.ActiveRunStatus = info.Status
	}
	return resp, nil
}

func sessionOwnedByAccount(h session.SessionHeader, accountID int) bool {
	return accountID <= 0 || h.AccountID == accountID || (h.AccountID == 0 && accountID == legacyWorkspaceAccountID)
}

const (
	sessionTypeChat = "chat"
	sessionTypeCode = "code"
)

func sessionTypeFromHeader(h session.SessionHeader) string {
	switch strings.ToLower(strings.TrimSpace(h.Type)) {
	case sessionTypeChat:
		return sessionTypeChat
	case sessionTypeCode, "coder":
		return sessionTypeCode
	}
	if h.WorkspaceID != "" {
		return sessionTypeCode
	}
	return sessionTypeChat
}

func sessionTypeForMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "coder") || strings.EqualFold(strings.TrimSpace(mode), sessionTypeCode) {
		return sessionTypeCode
	}
	return sessionTypeChat
}

func normalizeSessionTypeFilter(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", nil
	case sessionTypeChat:
		return sessionTypeChat, nil
	case sessionTypeCode:
		return sessionTypeCode, nil
	default:
		return "", fmt.Errorf("type must be chat or code")
	}
}

func sessionMetaFromHeader(h session.SessionHeader, messageCount int) SessionMeta {
	sessionType := sessionTypeFromHeader(h)
	platform := "chat"
	if sessionType == sessionTypeCode {
		platform = "coder"
	}
	return SessionMeta{
		ID:             h.ID,
		Title:          h.ID,
		Type:           sessionType,
		Platform:       platform,
		MessageCount:   messageCount,
		Model:          h.Model,
		CreatedAt:      h.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      h.UpdatedAt.Format(time.RFC3339),
		WorkspaceID:    h.WorkspaceID,
		ParentSession:  h.ParentSession,
		WorktreeBranch: h.WorktreeBranch,
		BaseCommit:     h.WorktreeBaseCommit,
	}
}

// ListSessions returns session headers newest-first.
func (s *Service) ListSessions() (SessionListResponse, error) {
	return s.listSessionsForAccount(0)
}

func (s *Service) listSessionsForAccount(accountID int, requestedTypes ...string) (SessionListResponse, error) {
	requestedType := ""
	if len(requestedTypes) > 0 {
		var err error
		requestedType, err = normalizeSessionTypeFilter(requestedTypes[0])
		if err != nil {
			return SessionListResponse{}, err
		}
	}
	headers, err := s.Store.List()
	if err != nil {
		return SessionListResponse{}, err
	}
	out := make([]SessionMeta, 0, len(headers))
	for _, h := range headers {
		if !sessionOwnedByAccount(h, accountID) || (requestedType != "" && sessionTypeFromHeader(h) != requestedType) {
			continue
		}
		out = append(out, s.sessionMeta(h))
	}
	return SessionListResponse{Sessions: out}, nil
}

func (s *Service) sessionMeta(h session.SessionHeader) SessionMeta {
	meta := sessionMetaFromHeader(h, 0)
	if _, entries, err := s.Store.LoadEntries(h.ID); err == nil {
		msgs := entriesToRestored(entries, h.Model)
		meta.MessageCount = len(msgs)
		meta.Title = sessionTitle(msgs)
		meta.Preview = meta.Title
	}
	if active := s.Runs.ActiveForSession(h.ID); active != nil {
		info := active.Info()
		meta.ActiveRunStatus = info.Status
		meta.WorkspaceID = info.WorkspaceID
	}
	return meta
}

func normalizeSessionScope(mode, workspaceID string) (string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	workspaceID = strings.TrimSpace(workspaceID)
	switch mode {
	case "chat":
		if workspaceID != "" {
			return "", "", fmt.Errorf("chat sessions cannot have a workspace")
		}
	case "coder":
		if workspaceID == "" {
			return "", "", fmt.Errorf("workspace_id is required for coder sessions")
		}
	default:
		return "", "", fmt.Errorf("mode must be chat or coder")
	}
	return mode, workspaceID, nil
}

func sessionMatchesScope(h session.SessionHeader, mode, workspaceID string) bool {
	if mode == "chat" {
		return sessionTypeFromHeader(h) == sessionTypeChat
	}
	return sessionTypeFromHeader(h) == sessionTypeCode && h.WorkspaceID == workspaceID
}

func sessionScopeMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "coder") {
		return "coder"
	}
	return "chat"
}

func (s *Service) setActiveSession(ctx context.Context, accountID int, mode, workspaceID, sessionID string) error {
	mode, workspaceID, err := normalizeSessionScope(mode, workspaceID)
	if err != nil {
		return err
	}
	if accountID <= 0 {
		return fmt.Errorf("account context is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	h, _, err := s.Store.LoadEntries(sessionID)
	if err != nil {
		return err
	}
	if !sessionOwnedByAccount(h, accountID) || !sessionMatchesScope(h, mode, workspaceID) {
		return fmt.Errorf("session not found: %w", os.ErrNotExist)
	}
	_, err = s.Audit.db.ExecContext(ctx, `
INSERT INTO account_active_sessions(account_id, mode, workspace_id, session_id, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(account_id, mode, workspace_id) DO UPDATE SET
    session_id = excluded.session_id,
    updated_at = excluded.updated_at`,
		accountID, mode, workspaceID, sessionID, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Service) clearActiveSession(ctx context.Context, accountID int, mode, workspaceID, sessionID string) error {
	mode, workspaceID, err := normalizeSessionScope(mode, workspaceID)
	if err != nil {
		return err
	}
	if sessionID != "" {
		_, err = s.Audit.db.ExecContext(ctx, `
DELETE FROM account_active_sessions
WHERE account_id = ? AND mode = ? AND workspace_id = ? AND session_id = ?`,
			accountID, mode, workspaceID, strings.TrimSpace(sessionID))
		return err
	}
	_, err = s.Audit.db.ExecContext(ctx,
		`DELETE FROM account_active_sessions WHERE account_id = ? AND mode = ? AND workspace_id = ?`,
		accountID, mode, workspaceID)
	return err
}

func (s *Service) getActiveSession(ctx context.Context, accountID int, mode, workspaceID string) (ActiveSessionResponse, bool, error) {
	if strings.EqualFold(strings.TrimSpace(mode), "coder") && strings.TrimSpace(workspaceID) == "" {
		return s.getLatestActiveCoderSession(ctx, accountID)
	}
	mode, workspaceID, err := normalizeSessionScope(mode, workspaceID)
	if err != nil {
		return ActiveSessionResponse{}, false, err
	}
	var sessionID string
	err = s.Audit.db.QueryRowContext(ctx, `
SELECT session_id FROM account_active_sessions
WHERE account_id = ? AND mode = ? AND workspace_id = ?`, accountID, mode, workspaceID).Scan(&sessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return ActiveSessionResponse{}, false, nil
		}
		return ActiveSessionResponse{}, false, err
	}
	h, _, loadErr := s.Store.LoadEntries(sessionID)
	if loadErr != nil && !isNotFound(loadErr) {
		return ActiveSessionResponse{}, false, loadErr
	}
	if loadErr != nil || !sessionOwnedByAccount(h, accountID) || !sessionMatchesScope(h, mode, workspaceID) {
		_, _ = s.Audit.db.ExecContext(ctx,
			`DELETE FROM account_active_sessions WHERE account_id = ? AND mode = ? AND workspace_id = ?`,
			accountID, mode, workspaceID)
		return ActiveSessionResponse{}, false, nil
	}
	return ActiveSessionResponse{SessionID: sessionID, Type: sessionTypeForMode(mode), Mode: mode, WorkspaceID: workspaceID}, true, nil
}

func (s *Service) getLatestActiveCoderSession(ctx context.Context, accountID int) (ActiveSessionResponse, bool, error) {
	rows, err := s.Audit.db.QueryContext(ctx, `
SELECT workspace_id, session_id FROM account_active_sessions
WHERE account_id = ? AND mode = 'coder'
ORDER BY updated_at DESC`, accountID)
	if err != nil {
		return ActiveSessionResponse{}, false, err
	}
	defer rows.Close()
	type candidate struct{ workspaceID, sessionID string }
	var candidates []candidate
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.workspaceID, &item.sessionID); err != nil {
			return ActiveSessionResponse{}, false, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return ActiveSessionResponse{}, false, err
	}
	if err := rows.Close(); err != nil {
		return ActiveSessionResponse{}, false, err
	}
	for _, item := range candidates {
		h, _, loadErr := s.Store.LoadEntries(item.sessionID)
		if loadErr != nil && !isNotFound(loadErr) {
			return ActiveSessionResponse{}, false, loadErr
		}
		if loadErr == nil && sessionOwnedByAccount(h, accountID) && sessionMatchesScope(h, "coder", item.workspaceID) {
			return ActiveSessionResponse{
				SessionID: item.sessionID, Type: sessionTypeCode, Mode: "coder", WorkspaceID: item.workspaceID,
			}, true, nil
		}
		_, _ = s.Audit.db.ExecContext(ctx, `
DELETE FROM account_active_sessions
WHERE account_id = ? AND mode = 'coder' AND workspace_id = ?`, accountID, item.workspaceID)
	}
	return ActiveSessionResponse{}, false, nil
}

func (s *Service) recentSessionsForAccount(accountID int, mode, workspaceID string, limit int) (SessionListResponse, error) {
	mode, workspaceID, err := normalizeSessionScope(mode, workspaceID)
	if err != nil {
		return SessionListResponse{}, err
	}
	if limit <= 0 || limit > 10 {
		limit = 10
	}
	headers, err := s.Store.List()
	if err != nil {
		return SessionListResponse{}, err
	}
	out := make([]SessionMeta, 0, min(limit, len(headers)))
	for _, h := range headers {
		if !sessionOwnedByAccount(h, accountID) || !sessionMatchesScope(h, mode, workspaceID) {
			continue
		}
		out = append(out, s.sessionMeta(h))
		if len(out) == limit {
			break
		}
	}
	return SessionListResponse{Sessions: out}, nil
}

func entriesToRestored(entries []session.Entry, model string) []RestoredMessage {
	out := make([]RestoredMessage, 0, len(entries))
	pendingArgs := map[string]string{}
	for _, e := range entries {
		switch m := e.Message.(type) {
		case agentcore.UserMessage:
			out = append(out, RestoredMessage{
				ID:        e.ID,
				Role:      "user",
				Content:   agentcore.ContentToText(m.Content),
				CreatedAt: e.Timestamp.Format(time.RFC3339),
			})
		case agentcore.AssistantMessage:
			for _, tc := range m.ToolCalls() {
				pendingArgs[tc.ID] = stringifyArgs(tc.Arguments)
			}
			out = append(out, RestoredMessage{
				ID:        e.ID,
				Role:      "assistant",
				Content:   agentcore.ContentToText(m.Content),
				Model:     model,
				CreatedAt: e.Timestamp.Format(time.RFC3339),
			})
		case agentcore.ToolResultMessage:
			if len(out) > 0 && out[len(out)-1].Role == "assistant" {
				step := ToolStep{
					Tool:   m.ToolName,
					Args:   pendingArgs[m.ToolCallID],
					Result: agentcore.ContentToText(m.Content),
					ID:     m.ToolCallID,
					Status: "done",
				}
				if m.IsError {
					step.Status = "error"
				}
				delete(pendingArgs, m.ToolCallID)
				last := &out[len(out)-1]
				last.ToolSteps = append(last.ToolSteps, step)
			}
		}
	}
	return out
}

func sessionTitle(msgs []RestoredMessage) string {
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
			t := strings.TrimSpace(m.Content)
			if len(t) > 64 {
				return t[:64] + "…"
			}
			return t
		}
	}
	return "session"
}

func stubAccount(username string) Account {
	if username == "" {
		username = "dev"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return Account{
		ID: 1, Username: username, Email: username + "@localhost", Role: "admin",
		CreatedAt: now, UpdatedAt: now,
	}
}

func parseAfterSeq(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
