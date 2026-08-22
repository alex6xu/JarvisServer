package gateway

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alex6xu/jarvisserver/internal/builtinskills"
	"github.com/alex6xu/jarvisserver/internal/runtime"
)

const maxSkillFileBytes = 256 << 10

var (
	errSkillNotFound = errors.New("skill not found")
	errSkillConflict = errors.New("skill revision conflict")
)

type SkillSummary struct {
	Name                   string   `json:"name"`
	Description            string   `json:"description"`
	AllowedTools           []string `json:"allowed_tools"`
	Source                 string   `json:"source"`
	Enabled                bool     `json:"enabled"`
	AccountEnabled         bool     `json:"account_enabled"`
	Revision               int64    `json:"revision"`
	ContentSHA256          string   `json:"content_sha256"`
	ValidationError        string   `json:"validation_error,omitempty"`
	DisableModelInvocation bool     `json:"disable_model_invocation"`
	UpdatedAt              string   `json:"updated_at"`
}

type SkillValidation struct {
	Valid    bool         `json:"valid"`
	Skill    SkillSummary `json:"skill"`
	Warnings []string     `json:"warnings"`
	Error    string       `json:"error,omitempty"`
}

type SkillSnapshot struct {
	Generation int64
	Skills     []*runtime.Skill
	Summaries  map[string]SkillSummary
}

type SkillReloadResult struct {
	Generation int64          `json:"generation"`
	Loaded     int            `json:"loaded"`
	Errors     []string       `json:"errors"`
	Skills     []SkillSummary `json:"skills"`
}

type SkillRegistryService struct {
	store      *GatewayStore
	dir        string
	knownTools map[string]bool

	mu         sync.RWMutex
	generation int64
	skills     map[string]*runtime.Skill
}

func NewSkillRegistryService(store *GatewayStore, dir string, knownTools []string) *SkillRegistryService {
	known := make(map[string]bool, len(knownTools))
	for _, name := range knownTools {
		known[name] = true
	}
	return &SkillRegistryService{store: store, dir: filepath.Clean(dir), knownTools: known, skills: make(map[string]*runtime.Skill)}
}

func (s *SkillRegistryService) Dir() string { return s.dir }

func (s *SkillRegistryService) Validate(_ context.Context, content []byte) SkillValidation {
	validation := SkillValidation{Warnings: []string{}}
	if len(content) == 0 {
		validation.Error = "skill content is required"
		return validation
	}
	if len(content) > maxSkillFileBytes {
		validation.Error = fmt.Sprintf("skill content exceeds %d bytes", maxSkillFileBytes)
		return validation
	}
	skill, err := runtime.ParseSkill("SKILL.md", content)
	if err != nil {
		validation.Error = err.Error()
		return validation
	}
	if strings.TrimSpace(skill.Body) == "" {
		validation.Error = "skill body is required"
		return validation
	}
	allowed := skillAllowedTools(skill)
	for _, name := range allowed {
		if !validToolReference(name) {
			validation.Error = fmt.Sprintf("invalid allowed tool name %q", name)
			return validation
		}
		if !s.knownTools[name] {
			validation.Warnings = append(validation.Warnings, fmt.Sprintf("tool %q is not currently registered", name))
		}
	}
	digest := sha256.Sum256(content)
	validation.Valid = true
	validation.Skill = SkillSummary{
		Name: skill.Frontmatter.Name, Description: skill.Frontmatter.Description,
		AllowedTools: allowed, Enabled: true, AccountEnabled: true,
		ContentSHA256:          hex.EncodeToString(digest[:]),
		DisableModelInvocation: skill.Frontmatter.DisableModelInvocation,
	}
	return validation
}

func (s *SkillRegistryService) Reload(ctx context.Context) (SkillReloadResult, error) {
	result := SkillReloadResult{Errors: []string{}, Skills: []SkillSummary{}}
	if s.dir == "." || strings.TrimSpace(s.dir) == "" {
		return result, errors.New("skills directory is not configured")
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			s.mu.Lock()
			s.skills = make(map[string]*runtime.Skill)
			s.generation++
			result.Generation = s.generation
			s.mu.Unlock()
			return result, nil
		}
		return result, err
	}
	loaded := make(map[string]*runtime.Skill)
	for _, entry := range entries {
		path := ""
		if entry.IsDir() {
			path = filepath.Join(s.dir, entry.Name(), "SKILL.md")
			info, statErr := os.Lstat(path)
			if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
		} else if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			info, infoErr := entry.Info()
			if infoErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				continue
			}
			path = filepath.Join(s.dir, entry.Name())
		} else {
			continue
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, readErr))
			s.markSkillPathError(ctx, path, readErr.Error())
			continue
		}
		validation := s.Validate(ctx, content)
		if !validation.Valid {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", path, validation.Error))
			s.markSkillPathError(ctx, path, validation.Error)
			continue
		}
		skill, _ := runtime.ParseSkill(path, content)
		if entry.IsDir() && skill.Frontmatter.Name != entry.Name() {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: skill name must match directory name", path))
			continue
		}
		if _, duplicate := loaded[skill.Frontmatter.Name]; duplicate {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: duplicate skill name %q", path, skill.Frontmatter.Name))
			continue
		}
		loaded[skill.Frontmatter.Name] = skill
		if err := s.syncSkillMetadata(ctx, skill, validation.Skill.ContentSHA256); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", path, err))
			delete(loaded, skill.Frontmatter.Name)
		}
	}
	if err := s.removeMissingSkillMetadata(ctx); err != nil {
		result.Errors = append(result.Errors, err.Error())
	}
	s.mu.Lock()
	s.skills = loaded
	s.generation++
	result.Generation = s.generation
	s.mu.Unlock()
	result.Loaded = len(loaded)
	result.Skills, err = s.List(ctx, 0)
	return result, err
}

func (s *SkillRegistryService) markSkillPathError(ctx context.Context, path, message string) {
	relative, err := filepath.Rel(s.dir, path)
	if err != nil || !pathInside(s.dir, path) {
		return
	}
	_, _ = s.store.db.ExecContext(ctx, `UPDATE skills SET validation_error=?, updated_at=? WHERE relative_path=?`,
		message, time.Now().UTC().Format(time.RFC3339Nano), filepath.ToSlash(relative))
}

func (s *SkillRegistryService) removeMissingSkillMetadata(ctx context.Context) error {
	rows, err := s.store.db.QueryContext(ctx, `SELECT name, relative_path FROM skills`)
	if err != nil {
		return err
	}
	type metadataPath struct{ name, relative string }
	entries := make([]metadataPath, 0)
	for rows.Next() {
		var entry metadataPath
		if err := rows.Scan(&entry.name, &entry.relative); err != nil {
			_ = rows.Close()
			return err
		}
		entries = append(entries, entry)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(s.dir, filepath.FromSlash(entry.relative))
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			if _, deleteErr := s.store.db.ExecContext(ctx, `DELETE FROM skills WHERE name=?`, entry.name); deleteErr != nil {
				return deleteErr
			}
		}
	}
	return nil
}

func (s *SkillRegistryService) syncSkillMetadata(ctx context.Context, skill *runtime.Skill, digest string) error {
	allowedJSON, _ := json.Marshal(skillAllowedTools(skill))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	relative, err := filepath.Rel(s.dir, skill.Path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("skill path escapes skills directory")
	}
	source := "file"
	if builtinskills.IsBuiltin(skill.Frontmatter.Name) {
		source = "builtin"
	}
	_, err = s.store.db.ExecContext(ctx, `
INSERT INTO skills(name, relative_path, description, allowed_tools_json, source, enabled,
 revision, content_sha256, validation_error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 1, 1, ?, '', ?, ?)
ON CONFLICT(name) DO UPDATE SET
 relative_path=excluded.relative_path, description=excluded.description,
 allowed_tools_json=excluded.allowed_tools_json,
	 source=CASE WHEN skills.source='custom' THEN skills.source ELSE excluded.source END,
 revision=CASE WHEN skills.content_sha256 <> excluded.content_sha256 THEN skills.revision + 1 ELSE skills.revision END,
 content_sha256=excluded.content_sha256, validation_error='', updated_at=excluded.updated_at`,
		skill.Frontmatter.Name, filepath.ToSlash(relative), skill.Frontmatter.Description,
		string(allowedJSON), source, digest, now, now)
	return err
}

func (s *SkillRegistryService) List(ctx context.Context, accountID int) ([]SkillSummary, error) {
	query := `
SELECT s.name, s.description, s.allowed_tools_json, s.source, s.enabled,
       COALESCE(a.enabled, 1), s.revision, s.content_sha256,
       s.validation_error, s.updated_at
FROM skills s LEFT JOIN account_skills a ON a.skill_name=s.name AND a.account_id=?
ORDER BY s.name`
	rows, err := s.store.db.QueryContext(ctx, query, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]SkillSummary, 0)
	for rows.Next() {
		var summary SkillSummary
		var allowedJSON string
		var enabled, accountEnabled int
		if err := rows.Scan(&summary.Name, &summary.Description, &allowedJSON, &summary.Source,
			&enabled, &accountEnabled, &summary.Revision, &summary.ContentSHA256,
			&summary.ValidationError, &summary.UpdatedAt); err != nil {
			return nil, err
		}
		summary.Enabled, summary.AccountEnabled = enabled != 0, accountEnabled != 0
		_ = json.Unmarshal([]byte(allowedJSON), &summary.AllowedTools)
		if skill := s.skills[summary.Name]; skill != nil {
			summary.DisableModelInvocation = skill.Frontmatter.DisableModelInvocation
		}
		result = append(result, summary)
	}
	return result, rows.Err()
}

func (s *SkillRegistryService) Get(ctx context.Context, accountID int, name string) (SkillSummary, string, error) {
	summaries, err := s.List(ctx, accountID)
	if err != nil {
		return SkillSummary{}, "", err
	}
	var summary SkillSummary
	found := false
	for _, candidate := range summaries {
		if candidate.Name == name {
			summary, found = candidate, true
			break
		}
	}
	if !found {
		return SkillSummary{}, "", errSkillNotFound
	}
	s.mu.RLock()
	skill := s.skills[name]
	s.mu.RUnlock()
	if skill == nil {
		return summary, "", errSkillNotFound
	}
	content, err := os.ReadFile(skill.Path)
	return summary, string(content), err
}

func (s *SkillRegistryService) Snapshot(ctx context.Context, accountID int) (SkillSnapshot, error) {
	summaries, err := s.List(ctx, accountID)
	if err != nil {
		return SkillSnapshot{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := SkillSnapshot{Generation: s.generation, Skills: []*runtime.Skill{}, Summaries: make(map[string]SkillSummary)}
	for _, summary := range summaries {
		if !summary.Enabled || !summary.AccountEnabled || summary.ValidationError != "" {
			continue
		}
		if skill := s.skills[summary.Name]; skill != nil {
			copySkill := *skill
			snapshot.Skills = append(snapshot.Skills, &copySkill)
			snapshot.Summaries[summary.Name] = summary
		}
	}
	sort.Slice(snapshot.Skills, func(i, j int) bool { return snapshot.Skills[i].Frontmatter.Name < snapshot.Skills[j].Frontmatter.Name })
	return snapshot, nil
}

func (s *SkillRegistryService) Create(ctx context.Context, accountID int, content []byte) (SkillSummary, error) {
	validation := s.Validate(ctx, content)
	if !validation.Valid {
		return SkillSummary{}, errors.New(validation.Error)
	}
	name := validation.Skill.Name
	path, err := s.managedSkillPath(name)
	if err != nil {
		return SkillSummary{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return SkillSummary{}, fmt.Errorf("skill %q already exists", name)
	} else if !os.IsNotExist(err) {
		return SkillSummary{}, err
	}
	if err := atomicWriteSkill(path, content); err != nil {
		return SkillSummary{}, err
	}
	if _, err := s.Reload(ctx); err != nil {
		return SkillSummary{}, err
	}
	_, _ = s.store.db.ExecContext(ctx, `UPDATE skills SET source='custom', created_by=? WHERE name=?`, accountID, name)
	summary, _, err := s.Get(ctx, accountID, name)
	return summary, err
}

func (s *SkillRegistryService) Update(ctx context.Context, accountID int, name string, revision int64, content []byte) (SkillSummary, error) {
	validation := s.Validate(ctx, content)
	if !validation.Valid {
		return SkillSummary{}, errors.New(validation.Error)
	}
	if validation.Skill.Name != name {
		return SkillSummary{}, errors.New("skill name cannot be changed")
	}
	var current int64
	var source, relative string
	if err := s.store.db.QueryRowContext(ctx, `SELECT revision, source, relative_path FROM skills WHERE name=?`, name).Scan(&current, &source, &relative); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SkillSummary{}, errSkillNotFound
		}
		return SkillSummary{}, err
	}
	if current != revision {
		return SkillSummary{}, errSkillConflict
	}
	if source == "builtin" {
		return SkillSummary{}, errors.New("builtin skills cannot be edited")
	}
	if filepath.Base(filepath.FromSlash(relative)) != "SKILL.md" {
		return SkillSummary{}, errors.New("flat skill files are read-only; migrate this skill to <name>/SKILL.md")
	}
	path, err := s.managedSkillPath(name)
	if err != nil {
		return SkillSummary{}, err
	}
	if err := atomicWriteSkill(path, content); err != nil {
		return SkillSummary{}, err
	}
	if _, err := s.Reload(ctx); err != nil {
		return SkillSummary{}, err
	}
	summary, _, err := s.Get(ctx, accountID, name)
	return summary, err
}

func (s *SkillRegistryService) SetGlobalEnabled(ctx context.Context, name string, enabled bool) error {
	result, err := s.store.db.ExecContext(ctx, `UPDATE skills SET enabled=?, updated_at=? WHERE name=?`, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano), name)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errSkillNotFound
	}
	return nil
}

func (s *SkillRegistryService) SetAccountEnabled(ctx context.Context, accountID int, name string, enabled bool) error {
	var exists int
	if err := s.store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE name=?`, name).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return errSkillNotFound
	}
	_, err := s.store.db.ExecContext(ctx, `
INSERT INTO account_skills(account_id, skill_name, enabled, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT(account_id, skill_name) DO UPDATE SET enabled=excluded.enabled, updated_at=excluded.updated_at`,
		accountID, name, boolInt(enabled), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *SkillRegistryService) Delete(ctx context.Context, name string, revision int64) error {
	var current int64
	var relative, source string
	if err := s.store.db.QueryRowContext(ctx, `SELECT revision, relative_path, source FROM skills WHERE name=?`, name).Scan(&current, &relative, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errSkillNotFound
		}
		return err
	}
	if current != revision {
		return errSkillConflict
	}
	if source == "builtin" {
		return errors.New("builtin skills cannot be deleted")
	}
	path := filepath.Join(s.dir, filepath.FromSlash(relative))
	if !pathInside(s.dir, path) {
		return errors.New("skill path escapes skills directory")
	}
	archiveDir := s.dir + ".archive"
	if err := os.MkdirAll(archiveDir, 0o750); err != nil {
		return err
	}
	sourcePath := path
	archiveName := name + "-" + time.Now().UTC().Format("20060102T150405.000000000")
	if filepath.Base(path) == "SKILL.md" {
		sourcePath = filepath.Dir(path)
	} else {
		archiveName += filepath.Ext(path)
	}
	archivePath := filepath.Join(archiveDir, archiveName)
	if err := os.Rename(sourcePath, archivePath); err != nil {
		return err
	}
	if _, err := s.store.db.ExecContext(ctx, `DELETE FROM skills WHERE name=?`, name); err != nil {
		_ = os.Rename(archivePath, sourcePath)
		return err
	}
	_, err := s.Reload(ctx)
	return err
}

func (s *SkillRegistryService) managedSkillPath(name string) (string, error) {
	if !validManagedSkillName(name) {
		return "", errors.New("invalid skill name")
	}
	path := filepath.Join(s.dir, name, "SKILL.md")
	if !pathInside(s.dir, path) {
		return "", errors.New("skill path escapes skills directory")
	}
	return path, nil
}

func atomicWriteSkill(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".skill-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func pathInside(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func validManagedSkillName(name string) bool {
	if name == "" || len(name) > 64 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return false
	}
	for _, char := range name {
		if char != '-' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func validToolReference(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for _, char := range name {
		if char != '_' && char != '-' && char != ':' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func skillAllowedTools(skill *runtime.Skill) []string {
	result := make([]string, 0, len(skill.Frontmatter.AllowedTools))
	for _, name := range skill.Frontmatter.AllowedTools {
		name = strings.TrimSpace(name)
		if name != "" {
			result = append(result, name)
		}
	}
	return result
}
