package gateway

import (
	"strconv"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

// GetSession builds the SessionDetailResponse for web session restore.
func (s *Service) GetSession(id string) (SessionDetailResponse, error) {
	h, entries, err := s.Store.LoadEntries(id)
	if err != nil {
		return SessionDetailResponse{}, err
	}
	msgs := entriesToRestored(entries, h.Model)
	resp := SessionDetailResponse{
		Session: SessionMeta{
			ID:           h.ID,
			Title:        sessionTitle(msgs),
			Platform:     "jarvis",
			MessageCount: len(msgs),
			Model:        h.Model,
			CreatedAt:    h.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    h.UpdatedAt.Format(time.RFC3339),
		},
		Messages: msgs,
	}
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

// ListSessions returns session headers newest-first.
func (s *Service) ListSessions() (SessionListResponse, error) {
	headers, err := s.Store.List()
	if err != nil {
		return SessionListResponse{}, err
	}
	out := make([]SessionMeta, 0, len(headers))
	for _, h := range headers {
		meta := SessionMeta{
			ID:        h.ID,
			Title:     h.ID,
			Platform:  "jarvis",
			Model:     h.Model,
			CreatedAt: h.CreatedAt.Format(time.RFC3339),
			UpdatedAt: h.UpdatedAt.Format(time.RFC3339),
		}
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
		out = append(out, meta)
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
