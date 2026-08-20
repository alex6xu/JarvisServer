package gateway

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type GitHubCredential struct {
	AccountID   int
	TokenCipher string
	Login       string
	AuthMethod  string
	UpdatedAt   string
}

func (s *GatewayStore) UpsertGitHubCredential(ctx context.Context, credential GitHubCredential) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO github_credentials(account_id, token_cipher, github_login, auth_method, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(account_id) DO UPDATE SET token_cipher=excluded.token_cipher,
github_login=excluded.github_login, auth_method=excluded.auth_method, updated_at=excluded.updated_at`,
		credential.AccountID, credential.TokenCipher, credential.Login, credential.AuthMethod, credential.UpdatedAt)
	return err
}

func (s *GatewayStore) GitHubCredential(ctx context.Context, accountID int) (GitHubCredential, error) {
	var credential GitHubCredential
	err := s.db.QueryRowContext(ctx, `
SELECT account_id, token_cipher, github_login, auth_method, updated_at
FROM github_credentials WHERE account_id = ?`, accountID).Scan(&credential.AccountID,
		&credential.TokenCipher, &credential.Login, &credential.AuthMethod, &credential.UpdatedAt)
	return credential, err
}

func (s *GatewayStore) DeleteGitHubCredential(ctx context.Context, accountID int) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM github_credentials WHERE account_id = ?`, accountID)
	return err
}

func (s *GatewayStore) CreateGitHubOAuthState(ctx context.Context, stateHash string, accountID int, returnPath string, expiresAt time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, _ = s.db.ExecContext(ctx, `DELETE FROM github_oauth_states WHERE expires_at <= ?`, now)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO github_oauth_states(state_hash, account_id, return_path, expires_at, created_at)
VALUES (?, ?, ?, ?, ?)`, stateHash, accountID, returnPath, expiresAt.UTC().Format(time.RFC3339Nano), now)
	return err
}

func (s *GatewayStore) ConsumeGitHubOAuthState(ctx context.Context, stateHash string, now time.Time) (int, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, "", err
	}
	defer tx.Rollback()
	var accountID int
	var returnPath, expiresRaw string
	if err := tx.QueryRowContext(ctx, `
SELECT account_id, return_path, expires_at FROM github_oauth_states WHERE state_hash = ?`, stateHash).
		Scan(&accountID, &returnPath, &expiresRaw); err != nil {
		return 0, "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM github_oauth_states WHERE state_hash = ?`, stateHash); err != nil {
		return 0, "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, "", err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || !now.Before(expiresAt) {
		return 0, "", errors.New("github oauth state expired")
	}
	return accountID, returnPath, nil
}

func isMissingGitHubCredential(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
