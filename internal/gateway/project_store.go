package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/session"
)

var projectSlugCleanup = regexp.MustCompile(`[^a-z0-9]+`)

type Project struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Slug              string `json:"slug"`
	Description       string `json:"description,omitempty"`
	Source            string `json:"source"`
	Status            string `json:"status"`
	LinkedWorkspaceID string `json:"linked_workspace_id,omitempty"`
	SessionCount      int    `json:"session_count"`
	MessageCount      int    `json:"message_count"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
	AccountID         int    `json:"-"`
}

type SessionProject struct {
	Project    Project `json:"project"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
	Pinned     bool    `json:"pinned"`
}

type ProjectDetail struct {
	Project  Project       `json:"project"`
	Sessions []SessionMeta `json:"sessions"`
	Tags     []Tag         `json:"tags"`
}

func normalizeProjectSlug(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = projectSlugCleanup.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "project"
	}
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	return slug
}

func (s *GatewayStore) CreateProject(ctx context.Context, accountID int, name, description string) (Project, error) {
	name = strings.TrimSpace(name)
	if accountID <= 0 || name == "" {
		return Project{}, errors.New("project name is required")
	}
	if len([]rune(name)) > 120 || len([]rune(description)) > 2000 {
		return Project{}, errors.New("project name or description is too long")
	}
	slug := normalizeProjectSlug(name)
	base := slug
	for suffix := 2; ; suffix++ {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE account_id=? AND slug=?`, accountID, slug).Scan(&exists)
		if err != nil {
			return Project{}, err
		}
		if exists == 0 {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, suffix)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	project := Project{ID: newID("project"), AccountID: accountID, Name: name, Slug: slug,
		Description: strings.TrimSpace(description), Source: "user", Status: "active", CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(id,account_id,name,slug,description,source,status,linked_workspace_id,created_at,updated_at) VALUES(?,?,?,?,?,?,'active','',?,?)`,
		project.ID, accountID, project.Name, project.Slug, project.Description, project.Source, now, now)
	return project, err
}

func (s *GatewayStore) ListProjects(ctx context.Context, accountID int) ([]Project, error) {
	if err := s.refreshProjectStats(ctx, accountID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,slug,description,source,status,linked_workspace_id,session_count,message_count,created_at,updated_at FROM projects WHERE account_id=? ORDER BY updated_at DESC,name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []Project{}
	for rows.Next() {
		var project Project
		project.AccountID = accountID
		if err := rows.Scan(&project.ID, &project.Name, &project.Slug, &project.Description, &project.Source,
			&project.Status, &project.LinkedWorkspaceID, &project.SessionCount, &project.MessageCount,
			&project.CreatedAt, &project.UpdatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *GatewayStore) ProjectByID(ctx context.Context, accountID int, id string) (Project, error) {
	if err := s.refreshProjectStats(ctx, accountID); err != nil {
		return Project{}, err
	}
	var project Project
	project.AccountID = accountID
	err := s.db.QueryRowContext(ctx, `SELECT id,name,slug,description,source,status,linked_workspace_id,session_count,message_count,created_at,updated_at FROM projects WHERE account_id=? AND id=?`, accountID, id).Scan(
		&project.ID, &project.Name, &project.Slug, &project.Description, &project.Source, &project.Status,
		&project.LinkedWorkspaceID, &project.SessionCount, &project.MessageCount, &project.CreatedAt, &project.UpdatedAt)
	return project, err
}

func (s *GatewayStore) AssignSessionProject(ctx context.Context, accountID int, sessionID, projectID, source string, confidence float64, pinned bool) error {
	header, _, err := s.LoadEntries(sessionID)
	if err != nil || !sessionOwnedByAccount(header, accountID) {
		return errors.New("session not found")
	}
	if _, err := s.ProjectByID(ctx, accountID, projectID); err != nil {
		return errors.New("project not found")
	}
	if source == "" {
		source = "user"
	}
	if confidence <= 0 || confidence > 1 {
		confidence = 1
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.db.ExecContext(ctx, `INSERT INTO session_projects(account_id,session_id,project_id,confidence,source,pinned,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET account_id=excluded.account_id,project_id=excluded.project_id,confidence=excluded.confidence,source=excluded.source,pinned=excluded.pinned,updated_at=excluded.updated_at`,
		accountID, sessionID, projectID, confidence, source, boolInt(pinned), now, now)
	if err == nil {
		_ = s.refreshProjectStats(ctx, accountID)
	}
	return err
}

func (s *GatewayStore) RemoveSessionProject(ctx context.Context, accountID int, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM session_projects WHERE account_id=? AND session_id=?`, accountID, sessionID)
	if err == nil {
		_ = s.refreshProjectStats(ctx, accountID)
	}
	return err
}

func (s *GatewayStore) SessionProject(ctx context.Context, accountID int, sessionID string) (SessionProject, error) {
	var result SessionProject
	var pinned int
	var projectID string
	err := s.db.QueryRowContext(ctx, `SELECT project_id,confidence,source,pinned FROM session_projects WHERE account_id=? AND session_id=?`, accountID, sessionID).Scan(&projectID, &result.Confidence, &result.Source, &pinned)
	if err != nil {
		return result, err
	}
	result.Pinned = pinned != 0
	result.Project, err = s.ProjectByID(ctx, accountID, projectID)
	return result, err
}

func (s *GatewayStore) EnsureWorkspaceProjectForSession(ctx context.Context, header session.SessionHeader) error {
	if header.AccountID <= 0 || header.WorkspaceID == "" || header.ID == "" {
		return nil
	}
	var pinned int
	err := s.db.QueryRowContext(ctx, `SELECT pinned FROM session_projects WHERE session_id=?`, header.ID).Scan(&pinned)
	if err == nil && pinned != 0 {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var workspaceName, githubName string
	if err := s.db.QueryRowContext(ctx, `SELECT name,github_full_name FROM workspace_metadata WHERE id=? AND account_id=?`, header.WorkspaceID, header.AccountID).Scan(&workspaceName, &githubName); err != nil {
		workspaceName = header.WorkspaceID
	}
	if githubName != "" {
		workspaceName = githubName
	}
	var projectID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM projects WHERE account_id=? AND linked_workspace_id=?`, header.AccountID, header.WorkspaceID).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		projectID = newID("project")
		slug := "workspace-" + strings.ToLower(header.WorkspaceID)
		if _, err := s.db.ExecContext(ctx, `INSERT INTO projects(id,account_id,name,slug,description,source,status,linked_workspace_id,created_at,updated_at) VALUES(?,?,?,?,?,'workspace','active',?,?,?)`,
			projectID, header.AccountID, workspaceName, slug, "由代码工作区自动创建", header.WorkspaceID, now, now); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return s.AssignSessionProject(ctx, header.AccountID, header.ID, projectID, "workspace", 1, false)
}

func (s *GatewayStore) ReconcileWorkspaceProjects(ctx context.Context, accountID int) (int, error) {
	headers, err := s.List()
	if err != nil {
		return 0, err
	}
	assigned := 0
	for _, header := range headers {
		if !sessionOwnedByAccount(header, accountID) || header.WorkspaceID == "" {
			continue
		}
		before, beforeErr := s.SessionProject(ctx, accountID, header.ID)
		if err := s.EnsureWorkspaceProjectForSession(ctx, header); err != nil {
			return assigned, err
		}
		after, afterErr := s.SessionProject(ctx, accountID, header.ID)
		if (beforeErr != nil || before.Project.ID != after.Project.ID) && afterErr == nil {
			assigned++
		}
	}
	return assigned, nil
}

func (s *GatewayStore) ProjectSessionIDs(ctx context.Context, accountID int, projectID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM session_projects WHERE account_id=? AND project_id=? ORDER BY updated_at DESC`, accountID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *GatewayStore) ProjectTags(ctx context.Context, accountID int, projectID string, limit int) ([]Tag, error) {
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.id,t.slug,t.name,t.kind,COUNT(*) AS uses,MAX(mt.updated_at) FROM session_projects sp JOIN message_tags mt ON mt.session_id=sp.session_id AND mt.account_id=sp.account_id JOIN account_tags t ON t.id=mt.tag_id WHERE sp.account_id=? AND sp.project_id=? GROUP BY t.id,t.slug,t.name,t.kind ORDER BY uses DESC LIMIT ?`, accountID, projectID, limit)
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

func (s *GatewayStore) refreshProjectStats(ctx context.Context, accountID int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET session_count=(SELECT COUNT(*) FROM session_projects sp WHERE sp.project_id=projects.id), message_count=(SELECT COUNT(*) FROM session_projects sp JOIN session_entries se ON se.session_id=sp.session_id WHERE sp.project_id=projects.id), updated_at=COALESCE((SELECT MAX(s.updated_at) FROM session_projects sp JOIN sessions s ON s.id=sp.session_id WHERE sp.project_id=projects.id),projects.updated_at) WHERE account_id=?`, accountID)
	return err
}
