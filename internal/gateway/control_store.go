package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

func initializeControlPlane(ctx context.Context, store ControlRepository, mem *MemStore, seedModel string) error {
	profiles, err := store.ListRouteProfiles(ctx)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		profiles = mem.listProfiles()
		for _, profile := range profiles {
			if err := store.UpsertRouteProfile(ctx, profile); err != nil {
				return err
			}
		}
	}
	mem.replaceProfiles(profiles)

	tasks, err := store.ListAgentTasks(ctx)
	if err != nil {
		return err
	}
	mem.replaceTasks(tasks)

	tags, err := store.ListTags(ctx)
	if err != nil {
		return err
	}
	if len(tags) == 0 {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		tags = []Tag{{ID: newID("tag"), Slug: "general", Name: "General", Kind: "topic", UpdatedAt: now}}
		if err := store.UpsertTag(ctx, tags[0]); err != nil {
			return err
		}
	}
	mem.replaceTags(tags)

	agentProfiles, err := store.ListAgentProfiles(ctx)
	if err != nil {
		return err
	}
	if len(agentProfiles) == 0 {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		agentProfiles = []AgentProfile{
			{ID: "profile_chat", Name: "Chat", Mode: "chat", Tools: []string{}, Config: map[string]any{"model": seedModel}, CreatedAt: now},
			{ID: "profile_code", Name: "Code", Mode: "coder", Tools: []string{"read", "write", "edit", "grep", "find", "bash"}, Config: map[string]any{"model": seedModel}, CreatedAt: now},
		}
		for _, profile := range agentProfiles {
			if err := store.UpsertAgentProfile(ctx, profile); err != nil {
				return err
			}
		}
	}
	return nil
}

func encodeJSON(value any, fallback string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(raw)
}

func (s *GatewayStore) ListRouteProfiles(ctx context.Context) ([]RouteProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, purpose, models_json FROM route_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []RouteProfile
	for rows.Next() {
		var profile RouteProfile
		var models string
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Purpose, &models); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(models), &profile.Models); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (s *GatewayStore) UpsertRouteProfile(ctx context.Context, profile RouteProfile) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO route_profiles(id, name, purpose, models_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, purpose=excluded.purpose,
models_json=excluded.models_json, updated_at=excluded.updated_at`, profile.ID, profile.Name,
		profile.Purpose, encodeJSON(profile.Models, "[]"), now, now)
	return err
}

func (s *GatewayStore) ListAgentTasks(ctx context.Context) ([]AgentTask, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, workspace_id, route_profile_id, type, prompt, status, result, error,
       tool_steps_json, created_at, finished_at
FROM agent_tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []AgentTask
	for rows.Next() {
		var task AgentTask
		var steps string
		var finished sql.NullString
		if err := rows.Scan(&task.ID, &task.WorkspaceID, &task.RouteProfileID, &task.Type,
			&task.Prompt, &task.Status, &task.Result, &task.Error, &steps, &task.CreatedAt, &finished); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(steps), &task.ToolSteps); err != nil {
			return nil, err
		}
		if finished.Valid {
			task.FinishedAt = finished.String
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *GatewayStore) UpsertAgentTask(ctx context.Context, task AgentTask) error {
	var finished any
	if task.FinishedAt != "" {
		finished = task.FinishedAt
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_tasks(id, workspace_id, route_profile_id, type, prompt, status, result, error,
                        tool_steps_json, created_at, finished_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET workspace_id=excluded.workspace_id,
route_profile_id=excluded.route_profile_id, type=excluded.type, prompt=excluded.prompt,
status=excluded.status, result=excluded.result, error=excluded.error,
tool_steps_json=excluded.tool_steps_json, finished_at=excluded.finished_at`, task.ID,
		task.WorkspaceID, task.RouteProfileID, task.Type, task.Prompt, task.Status, task.Result,
		task.Error, encodeJSON(task.ToolSteps, "[]"), task.CreatedAt, finished)
	return err
}

func (s *GatewayStore) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, slug, name, kind, use_count, updated_at FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []Tag
	for rows.Next() {
		var tag Tag
		if err := rows.Scan(&tag.ID, &tag.Slug, &tag.Name, &tag.Kind, &tag.UseCount, &tag.UpdatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *GatewayStore) UpsertTag(ctx context.Context, tag Tag) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tags(id, slug, name, kind, use_count, updated_at) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET slug=excluded.slug, name=excluded.name, kind=excluded.kind,
use_count=excluded.use_count, updated_at=excluded.updated_at`, tag.ID, tag.Slug, tag.Name,
		tag.Kind, tag.UseCount, tag.UpdatedAt)
	return err
}

func (s *GatewayStore) UpsertWorkspace(ctx context.Context, workspace WorkspaceInfo) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO workspace_metadata(id, name, source, github_full_name, github_default_branch, created_at, updated_at, account_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, source=excluded.source,
github_full_name=excluded.github_full_name, github_default_branch=excluded.github_default_branch,
updated_at=excluded.updated_at, account_id=excluded.account_id`, workspace.ID, workspace.Name, workspace.Source,
		workspace.GitHubFullName, workspace.GitHubDefaultBranch, workspace.CreatedAt, workspace.UpdatedAt,
		workspace.AccountID)
	return err
}

func (s *GatewayStore) WorkspaceAccountID(ctx context.Context, id string) (int, error) {
	var accountID int
	err := s.db.QueryRowContext(ctx, `SELECT account_id FROM workspace_metadata WHERE id = ?`, id).Scan(&accountID)
	return accountID, err
}

func (s *GatewayStore) DeleteWorkspace(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM workspace_metadata WHERE id = ?`, id)
	return err
}

func (s *GatewayStore) ListAgentProfiles(ctx context.Context) ([]AgentProfile, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, mode, system_prompt, tools_json, config_json, created_at, updated_at
FROM agent_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var profiles []AgentProfile
	for rows.Next() {
		var profile AgentProfile
		var tools, config string
		if err := rows.Scan(&profile.ID, &profile.Name, &profile.Mode, &profile.SystemPrompt,
			&tools, &config, &profile.CreatedAt, &profile.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tools), &profile.Tools); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(config), &profile.Config); err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func (s *GatewayStore) UpsertAgentProfile(ctx context.Context, profile AgentProfile) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if profile.CreatedAt == "" {
		profile.CreatedAt = now
	}
	profile.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO agent_profiles(id, name, mode, system_prompt, tools_json, config_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, mode=excluded.mode,
system_prompt=excluded.system_prompt, tools_json=excluded.tools_json,
config_json=excluded.config_json, updated_at=excluded.updated_at`, profile.ID, profile.Name,
		profile.Mode, profile.SystemPrompt, encodeJSON(profile.Tools, "[]"),
		encodeJSON(profile.Config, "{}"), profile.CreatedAt, profile.UpdatedAt)
	return err
}

func (s *GatewayStore) ListChannelBindings(ctx context.Context) ([]ChannelBinding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, channel, account_id, conversation, agent_profile_id, workspace_id, created_at, updated_at
FROM channel_bindings ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var bindings []ChannelBinding
	for rows.Next() {
		var binding ChannelBinding
		if err := rows.Scan(&binding.ID, &binding.Channel, &binding.AccountID, &binding.Conversation,
			&binding.AgentProfileID, &binding.WorkspaceID, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s *GatewayStore) UpsertChannelBinding(ctx context.Context, binding ChannelBinding) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if binding.CreatedAt == "" {
		binding.CreatedAt = now
	}
	binding.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
INSERT INTO channel_bindings(id, channel, account_id, conversation, agent_profile_id, workspace_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET channel=excluded.channel, account_id=excluded.account_id,
conversation=excluded.conversation, agent_profile_id=excluded.agent_profile_id,
workspace_id=excluded.workspace_id, updated_at=excluded.updated_at`, binding.ID, binding.Channel,
		binding.AccountID, binding.Conversation, binding.AgentProfileID, binding.WorkspaceID,
		binding.CreatedAt, binding.UpdatedAt)
	return err
}
