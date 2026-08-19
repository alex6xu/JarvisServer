package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alex6xu/jarvisserver/internal/agentcore"
	"github.com/alex6xu/jarvisserver/internal/session"
)

// SessionRepository is the persistence boundary used by Gateway chat/session flows.
type SessionRepository interface {
	Save(session.SessionHeader, agentcore.MessageList) error
	SaveEntries(session.SessionHeader, []session.Entry) error
	LoadEntries(string) (session.SessionHeader, []session.Entry, error)
	List() ([]session.SessionHeader, error)
	AppendBranch(session.SessionHeader, string, agentcore.MessageList) (string, error)
}

// ControlRepository persists mutable Gateway control-plane configuration.
type ControlRepository interface {
	ListRouteProfiles(context.Context) ([]RouteProfile, error)
	UpsertRouteProfile(context.Context, RouteProfile) error
	ListAgentTasks(context.Context) ([]AgentTask, error)
	UpsertAgentTask(context.Context, AgentTask) error
	ListTags(context.Context) ([]Tag, error)
	UpsertTag(context.Context, Tag) error
	UpsertWorkspace(context.Context, WorkspaceInfo) error
	DeleteWorkspace(context.Context, string) error
	ListAgentProfiles(context.Context) ([]AgentProfile, error)
	UpsertAgentProfile(context.Context, AgentProfile) error
	ListChannelBindings(context.Context) ([]ChannelBinding, error)
	UpsertChannelBinding(context.Context, ChannelBinding) error
}

type AgentProfile struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Mode         string         `json:"mode"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	Tools        []string       `json:"tools"`
	Config       map[string]any `json:"config,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
}

type ChannelBinding struct {
	ID             string `json:"id"`
	Channel        string `json:"channel"`
	AccountID      int    `json:"account_id"`
	Conversation   string `json:"conversation"`
	AgentProfileID string `json:"agent_profile_id"`
	WorkspaceID    string `json:"workspace_id,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func (s *GatewayStore) Save(header session.SessionHeader, messages agentcore.MessageList) error {
	now := time.Now().UTC()
	entries := make([]session.Entry, 0, len(messages))
	parent := ""
	for _, message := range messages {
		entry := session.Entry{ID: newID("msg"), ParentID: parent, Timestamp: now, Message: message}
		entries = append(entries, entry)
		parent = entry.ID
	}
	return s.SaveEntries(header, entries)
}

func (s *GatewayStore) SaveEntries(header session.SessionHeader, entries []session.Entry) error {
	if header.ID == "" {
		return fmt.Errorf("session id is required")
	}
	header.Version = session.SchemaVersion
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
INSERT INTO sessions(id, header_json, model, provider, cwd, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET header_json=excluded.header_json, model=excluded.model,
provider=excluded.provider, cwd=excluded.cwd, updated_at=excluded.updated_at`,
		header.ID, string(headerJSON), header.Model, header.Provider, header.Cwd,
		header.CreatedAt.UTC().Format(time.RFC3339Nano), header.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM session_entries WHERE session_id = ?`, header.ID); err != nil {
		return err
	}
	for seq, entry := range entries {
		payload, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
INSERT INTO session_entries(session_id, entry_id, parent_id, seq, payload, created_at)
VALUES (?, ?, ?, ?, ?, ?)`, header.ID, entry.ID, entry.ParentID, seq+1, string(payload),
			entry.Timestamp.UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *GatewayStore) LoadEntries(id string) (session.SessionHeader, []session.Entry, error) {
	var headerJSON string
	if err := s.db.QueryRow(`SELECT header_json FROM sessions WHERE id = ?`, id).Scan(&headerJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return session.SessionHeader{}, nil, fmt.Errorf("session %s: %w", id, os.ErrNotExist)
		}
		return session.SessionHeader{}, nil, err
	}
	var header session.SessionHeader
	if err := json.Unmarshal([]byte(headerJSON), &header); err != nil {
		return session.SessionHeader{}, nil, err
	}
	rows, err := s.db.Query(`SELECT payload FROM session_entries WHERE session_id = ? ORDER BY seq`, id)
	if err != nil {
		return session.SessionHeader{}, nil, err
	}
	defer rows.Close()
	var entries []session.Entry
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return session.SessionHeader{}, nil, err
		}
		var entry session.Entry
		if err := json.Unmarshal([]byte(payload), &entry); err != nil {
			return session.SessionHeader{}, nil, err
		}
		entries = append(entries, entry)
	}
	return header, entries, rows.Err()
}

func (s *GatewayStore) List() ([]session.SessionHeader, error) {
	rows, err := s.db.Query(`SELECT header_json FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var headers []session.SessionHeader
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var header session.SessionHeader
		if err := json.Unmarshal([]byte(raw), &header); err != nil {
			return nil, err
		}
		headers = append(headers, header)
	}
	return headers, rows.Err()
}

func (s *GatewayStore) AppendBranch(header session.SessionHeader, parentLeafID string, messages agentcore.MessageList) (string, error) {
	_, entries, err := s.LoadEntries(header.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	now := time.Now().UTC()
	leaf := parentLeafID
	for _, message := range messages {
		entry := session.Entry{ID: newID("msg"), ParentID: leaf, Timestamp: now, Message: message}
		entries = append(entries, entry)
		leaf = entry.ID
	}
	if err := s.SaveEntries(header, entries); err != nil {
		return "", err
	}
	return leaf, nil
}

func importLegacySessions(legacy *session.Store, target SessionRepository) error {
	existing, err := target.List()
	if err != nil || len(existing) > 0 {
		return err
	}
	headers, err := legacy.List()
	if err != nil {
		return err
	}
	for _, header := range headers {
		loadedHeader, entries, err := legacy.LoadEntries(header.ID)
		if err != nil {
			return err
		}
		if err := target.SaveEntries(loadedHeader, entries); err != nil {
			return err
		}
	}
	return nil
}
