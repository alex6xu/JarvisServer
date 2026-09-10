package gateway

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

// sessionSummaryRepository avoids restoring tool results and assistant bodies
// just to render a list. Callers must check header ownership before using it.
// No cache is used: edits, branches and deletions are visible immediately.
type sessionSummaryRepository interface {
	SessionSummary(string) (int, string, error)
}

func (s *GatewayStore) SessionSummary(id string) (int, string, error) {
	// Project only the discriminator and user payload. SQLite still scans the
	// session's JSON, but large tool/assistant bodies never cross into Go or get
	// decoded into a second restored conversation. The session/seq index bounds
	// this query to one session and preserves the existing first-user title rule.
	rows, err := s.db.Query(`SELECT json_extract(payload, '$.message.role'),
 CASE WHEN json_extract(payload, '$.message.role') = 'user' THEN payload ELSE '' END
 FROM session_entries WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return 0, "", err
	}
	defer rows.Close()
	count, title := 0, "session"
	foundTitle := false
	for rows.Next() {
		var role, raw string
		if err := rows.Scan(&role, &raw); err != nil {
			return 0, "", err
		}
		switch role {
		case agentcore.RoleUser, agentcore.RoleAssistant, agentcore.RoleToolResult, agentcore.RoleCompaction:
		default:
			return 0, "", fmt.Errorf("invalid session message role")
		}
		if role == agentcore.RoleUser || role == agentcore.RoleAssistant {
			count++
		}
		if role != agentcore.RoleUser || foundTitle {
			continue
		}
		var entry session.Entry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return 0, "", err
		}
		m, ok := entry.Message.(agentcore.UserMessage)
		if ok && strings.TrimSpace(agentcore.ContentToText(m.Content)) != "" {
			title = sessionTitle([]RestoredMessage{{Role: "user", Content: agentcore.ContentToText(m.Content)}})
			foundTitle = true
		}
	}
	return count, title, rows.Err()
}
