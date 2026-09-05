package gateway

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TipTypeIdea     = "idea"
	TipTypeTodo     = "todo"
	TipTypeQuestion = "question"
	TipTypeNote     = "note"

	TipStatusInbox    = "inbox"
	TipStatusPlanned  = "planned"
	TipStatusDoing    = "doing"
	TipStatusDone     = "done"
	TipStatusArchived = "archived"
)

var ErrTipVersionConflict = errors.New("tip was updated by another request")

type ProjectTip struct {
	ID          string `json:"id"`
	ProjectID   string `json:"project_id"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Title       string `json:"title,omitempty"`
	Content     string `json:"content"`
	Priority    int    `json:"priority"`
	Source      string `json:"source"`
	DueAt       string `json:"due_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Position    int    `json:"position"`
	Version     int    `json:"version"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ArchivedAt  string `json:"archived_at,omitempty"`

	AccountID int `json:"-"`
}

type CreateProjectTipInput struct {
	ProjectID string
	Type      string
	Title     string
	Content   string
	Priority  int
	DueAt     string
}

type UpdateProjectTipInput struct {
	ProjectID string
	ID        string
	Version   int
	Type      *string
	Status    *string
	Title     *string
	Content   *string
	Priority  *int
	DueAt     *string
}

type ProjectTipListOptions struct {
	Status string
	Type   string
	Query  string
	Limit  int
}

const projectTipColumns = `id,account_id,project_id,type,status,title,content,priority,source,due_at,completed_at,position,version,created_at,updated_at,archived_at`

func scanProjectTip(scanner interface{ Scan(...any) error }) (ProjectTip, error) {
	var tip ProjectTip
	err := scanner.Scan(&tip.ID, &tip.AccountID, &tip.ProjectID, &tip.Type, &tip.Status, &tip.Title,
		&tip.Content, &tip.Priority, &tip.Source, &tip.DueAt, &tip.CompletedAt, &tip.Position,
		&tip.Version, &tip.CreatedAt, &tip.UpdatedAt, &tip.ArchivedAt)
	return tip, err
}

func validTipType(value string) bool {
	switch value {
	case TipTypeIdea, TipTypeTodo, TipTypeQuestion, TipTypeNote:
		return true
	default:
		return false
	}
}

func validTipStatus(value string) bool {
	switch value {
	case TipStatusInbox, TipStatusPlanned, TipStatusDoing, TipStatusDone, TipStatusArchived:
		return true
	default:
		return false
	}
}

func validateTipText(title, content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("tip content is required")
	}
	if utf8.RuneCountInString(title) > 200 {
		return errors.New("tip title is too long")
	}
	if utf8.RuneCountInString(content) > 20000 {
		return errors.New("tip content is too long")
	}
	return nil
}

func normalizeTipTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", errors.New("tip due_at must be RFC3339")
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func (s *GatewayStore) CreateProjectTip(ctx context.Context, accountID int, input CreateProjectTipInput) (ProjectTip, error) {
	input.ProjectID = strings.TrimSpace(input.ProjectID)
	input.Type = strings.TrimSpace(input.Type)
	input.Title = strings.TrimSpace(input.Title)
	input.Content = strings.TrimSpace(input.Content)
	if input.Type == "" {
		input.Type = TipTypeNote
	}
	if accountID <= 0 || input.ProjectID == "" {
		return ProjectTip{}, errors.New("project is required")
	}
	if !validTipType(input.Type) {
		return ProjectTip{}, errors.New("invalid tip type")
	}
	if input.Priority < 0 || input.Priority > 3 {
		return ProjectTip{}, errors.New("invalid tip priority")
	}
	if err := validateTipText(input.Title, input.Content); err != nil {
		return ProjectTip{}, err
	}
	dueAt, err := normalizeTipTime(input.DueAt)
	if err != nil {
		return ProjectTip{}, err
	}
	if _, err := s.ProjectByID(ctx, accountID, input.ProjectID); err != nil {
		return ProjectTip{}, sql.ErrNoRows
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tip := ProjectTip{
		ID: newID("tip"), AccountID: accountID, ProjectID: input.ProjectID, Type: input.Type,
		Status: TipStatusInbox, Title: input.Title, Content: input.Content, Priority: input.Priority,
		Source: "user", DueAt: dueAt, Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),-1)+1 FROM project_tips WHERE account_id=? AND project_id=?`, accountID, input.ProjectID).Scan(&tip.Position)
	if err != nil {
		return ProjectTip{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO project_tips(`+projectTipColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		tip.ID, tip.AccountID, tip.ProjectID, tip.Type, tip.Status, tip.Title, tip.Content, tip.Priority,
		tip.Source, tip.DueAt, tip.CompletedAt, tip.Position, tip.Version, tip.CreatedAt, tip.UpdatedAt, tip.ArchivedAt)
	if err != nil {
		return ProjectTip{}, err
	}
	return tip, nil
}

func (s *GatewayStore) ProjectTipByID(ctx context.Context, accountID int, projectID, tipID string) (ProjectTip, error) {
	return scanProjectTip(s.db.QueryRowContext(ctx, `SELECT `+projectTipColumns+` FROM project_tips WHERE account_id=? AND project_id=? AND id=?`,
		accountID, strings.TrimSpace(projectID), strings.TrimSpace(tipID)))
}

func (s *GatewayStore) ListProjectTips(ctx context.Context, accountID int, projectID string, opts ProjectTipListOptions) ([]ProjectTip, error) {
	projectID = strings.TrimSpace(projectID)
	if _, err := s.ProjectByID(ctx, accountID, projectID); err != nil {
		return nil, sql.ErrNoRows
	}
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 100
	}
	query := `SELECT ` + projectTipColumns + ` FROM project_tips WHERE account_id=? AND project_id=?`
	args := []any{accountID, projectID}
	if opts.Status != "" {
		if !validTipStatus(opts.Status) {
			return nil, errors.New("invalid tip status")
		}
		query += ` AND status=?`
		args = append(args, opts.Status)
	}
	if opts.Type != "" {
		if !validTipType(opts.Type) {
			return nil, errors.New("invalid tip type")
		}
		query += ` AND type=?`
		args = append(args, opts.Type)
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		q = strings.ReplaceAll(strings.ReplaceAll(q, `\`, `\\`), `%`, `\%`)
		q = strings.ReplaceAll(q, `_`, `\_`)
		query += ` AND (title LIKE ? ESCAPE '\' OR content LIKE ? ESCAPE '\')`
		args = append(args, "%"+q+"%", "%"+q+"%")
	}
	query += ` ORDER BY CASE status WHEN 'doing' THEN 0 WHEN 'planned' THEN 1 WHEN 'inbox' THEN 2 WHEN 'done' THEN 3 ELSE 4 END, priority DESC, position, updated_at DESC LIMIT ?`
	args = append(args, opts.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tips := make([]ProjectTip, 0)
	for rows.Next() {
		tip, err := scanProjectTip(rows)
		if err != nil {
			return nil, err
		}
		tips = append(tips, tip)
	}
	return tips, rows.Err()
}

func (s *GatewayStore) UpdateProjectTip(ctx context.Context, accountID int, input UpdateProjectTipInput) (ProjectTip, error) {
	if input.Version <= 0 {
		return ProjectTip{}, errors.New("tip version is required")
	}
	current, err := s.ProjectTipByID(ctx, accountID, input.ProjectID, input.ID)
	if err != nil {
		return ProjectTip{}, err
	}
	if input.Type != nil {
		current.Type = strings.TrimSpace(*input.Type)
		if !validTipType(current.Type) {
			return ProjectTip{}, errors.New("invalid tip type")
		}
	}
	if input.Status != nil {
		current.Status = strings.TrimSpace(*input.Status)
		if !validTipStatus(current.Status) {
			return ProjectTip{}, errors.New("invalid tip status")
		}
	}
	if input.Title != nil {
		current.Title = strings.TrimSpace(*input.Title)
	}
	if input.Content != nil {
		current.Content = strings.TrimSpace(*input.Content)
	}
	if input.Priority != nil {
		current.Priority = *input.Priority
		if current.Priority < 0 || current.Priority > 3 {
			return ProjectTip{}, errors.New("invalid tip priority")
		}
	}
	if input.DueAt != nil {
		current.DueAt, err = normalizeTipTime(*input.DueAt)
		if err != nil {
			return ProjectTip{}, err
		}
	}
	if err := validateTipText(current.Title, current.Content); err != nil {
		return ProjectTip{}, err
	}

	now := time.Now().UTC()
	if input.Status != nil {
		current.CompletedAt = ""
		current.ArchivedAt = ""
		if current.Status == TipStatusDone {
			current.CompletedAt = now.Format(time.RFC3339Nano)
		}
		if current.Status == TipStatusArchived {
			current.ArchivedAt = now.Format(time.RFC3339Nano)
		}
	}
	current.UpdatedAt = now.Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE project_tips SET type=?,status=?,title=?,content=?,priority=?,due_at=?,completed_at=?,version=version+1,updated_at=?,archived_at=? WHERE account_id=? AND project_id=? AND id=? AND version=?`,
		current.Type, current.Status, current.Title, current.Content, current.Priority, current.DueAt,
		current.CompletedAt, current.UpdatedAt, current.ArchivedAt, accountID, current.ProjectID, current.ID, input.Version)
	if err != nil {
		return ProjectTip{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ProjectTip{}, ErrTipVersionConflict
	}
	current.Version = input.Version + 1
	return current, nil
}

func (s *GatewayStore) DeleteProjectTip(ctx context.Context, accountID int, projectID, tipID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM project_tips WHERE account_id=? AND project_id=? AND id=?`, accountID, strings.TrimSpace(projectID), strings.TrimSpace(tipID))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return fmt.Errorf("tip not found: %w", sql.ErrNoRows)
	}
	return nil
}
