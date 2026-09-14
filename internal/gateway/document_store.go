package gateway

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	DocumentStatusProcessing = "processing"
	DocumentStatusReady      = "ready"
	DocumentStatusFailed     = "failed"
	DocumentStatusDeleted    = "deleted"
)

// ProjectDocument is project-scoped metadata. Server-side storage paths are
// deliberately omitted from JSON responses.
type ProjectDocument struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	Filename       string `json:"filename"`
	MIMEType       string `json:"mime_type"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256"`
	Status         string `json:"status"`
	ExtractedBytes int64  `json:"extracted_bytes"`
	Parser         string `json:"parser,omitempty"`
	ParserVersion  string `json:"parser_version,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	MetadataJSON   string `json:"metadata_json,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	DeletedAt      string `json:"deleted_at,omitempty"`

	AccountID         int    `json:"-"`
	StoragePath       string `json:"-"`
	ExtractedTextPath string `json:"-"`
}

type ProjectDocumentListOptions struct {
	Status string
	Limit  int
	Cursor string
}

func scanProjectDocument(scanner interface{ Scan(...any) error }) (ProjectDocument, error) {
	var d ProjectDocument
	err := scanner.Scan(&d.ID, &d.AccountID, &d.ProjectID, &d.Filename, &d.MIMEType, &d.SizeBytes,
		&d.SHA256, &d.Status, &d.StoragePath, &d.ExtractedTextPath, &d.ExtractedBytes,
		&d.Parser, &d.ParserVersion, &d.ErrorCode, &d.MetadataJSON, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt)
	return d, err
}

const projectDocumentColumns = `id,account_id,project_id,filename,mime_type,size_bytes,sha256,status,storage_path,extracted_text_path,extracted_bytes,parser,parser_version,error_code,metadata_json,created_at,updated_at,deleted_at`

func (s *GatewayStore) CreateProjectDocument(ctx context.Context, d ProjectDocument) error {
	if d.AccountID <= 0 || d.ProjectID == "" || d.ID == "" {
		return errors.New("invalid project document")
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=? AND account_id=?`, d.ProjectID, d.AccountID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return sql.ErrNoRows
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO project_documents(`+projectDocumentColumns+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.AccountID, d.ProjectID, d.Filename, d.MIMEType, d.SizeBytes, d.SHA256, d.Status,
		d.StoragePath, d.ExtractedTextPath, d.ExtractedBytes, d.Parser, d.ParserVersion,
		d.ErrorCode, d.MetadataJSON, d.CreatedAt, d.UpdatedAt, d.DeletedAt)
	return err
}

func (s *GatewayStore) ProjectDocumentByID(ctx context.Context, accountID int, projectID, documentID string, includeDeleted bool) (ProjectDocument, error) {
	query := `SELECT ` + projectDocumentColumns + ` FROM project_documents WHERE account_id=? AND project_id=? AND id=?`
	if !includeDeleted {
		query += ` AND deleted_at=''`
	}
	return scanProjectDocument(s.db.QueryRowContext(ctx, query, accountID, projectID, documentID))
}

func (s *GatewayStore) ListProjectDocuments(ctx context.Context, accountID int, projectID string, opts ProjectDocumentListOptions) ([]ProjectDocument, error) {
	if _, err := s.ProjectByID(ctx, accountID, projectID); err != nil {
		return nil, sql.ErrNoRows
	}
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}
	query := `SELECT ` + projectDocumentColumns + ` FROM project_documents WHERE account_id=? AND project_id=? AND deleted_at=''`
	args := []any{accountID, projectID}
	if opts.Status != "" {
		query += ` AND status=?`
		args = append(args, opts.Status)
	}
	if opts.Cursor != "" {
		query += ` AND created_at<?`
		args = append(args, opts.Cursor)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, opts.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	docs := make([]ProjectDocument, 0)
	for rows.Next() {
		d, err := scanProjectDocument(rows)
		if err != nil {
			return nil, err
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

func (s *GatewayStore) ProjectDocumentUsage(ctx context.Context, accountID int, projectID string) (int64, error) {
	var total int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes),0) FROM project_documents WHERE account_id=? AND project_id=? AND deleted_at=''`, accountID, projectID).Scan(&total)
	return total, err
}

func (s *GatewayStore) CompleteProjectDocument(ctx context.Context, accountID int, projectID, documentID, extractedPath string, extractedBytes int64, parser string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE project_documents SET status=?,extracted_text_path=?,extracted_bytes=?,parser=?,parser_version='1',error_code='',updated_at=? WHERE account_id=? AND project_id=? AND id=? AND deleted_at=''`,
		DocumentStatusReady, extractedPath, extractedBytes, parser, now, accountID, projectID, documentID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *GatewayStore) FailProjectDocument(ctx context.Context, accountID int, projectID, documentID, code string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `UPDATE project_documents SET status=?,error_code=?,updated_at=? WHERE account_id=? AND project_id=? AND id=? AND deleted_at=''`, DocumentStatusFailed, code, now, accountID, projectID, documentID)
	return err
}

func (s *GatewayStore) DeleteProjectDocument(ctx context.Context, accountID int, projectID, documentID string) (ProjectDocument, error) {
	d, err := s.ProjectDocumentByID(ctx, accountID, projectID, documentID, false)
	if err != nil {
		return ProjectDocument{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `UPDATE project_documents SET status=?,deleted_at=?,updated_at=? WHERE account_id=? AND project_id=? AND id=? AND deleted_at=''`, DocumentStatusDeleted, now, now, accountID, projectID, documentID)
	if err != nil {
		return ProjectDocument{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ProjectDocument{}, sql.ErrNoRows
	}
	d.Status, d.DeletedAt, d.UpdatedAt = DocumentStatusDeleted, now, now
	return d, nil
}
