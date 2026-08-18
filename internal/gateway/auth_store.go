package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const passwordIterations = 210_000

func passwordHash(password string) (string, error) {
	if len(password) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := pbkdf2SHA256([]byte(password), salt, passwordIterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func passwordMatches(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > 1_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func pbkdf2SHA256(password, salt []byte, iterations, keyLen int) []byte {
	result := make([]byte, 0, keyLen)
	for block := uint32(1); len(result) < keyLen; block++ {
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		result = append(result, t...)
	}
	return result[:keyLen]
}

func randomToken(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *GatewayStore) AccountCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts`).Scan(&count)
	return count, err
}

func (s *GatewayStore) CreateAccount(ctx context.Context, username, email, role, password string) (Account, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Account{}, errors.New("username is required")
	}
	if role != "admin" && role != "user" {
		role = "user"
	}
	hash, err := passwordHash(password)
	if err != nil {
		return Account{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx, `
INSERT INTO accounts(username, email, role, password_hash, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, username, strings.TrimSpace(email), role, hash, now, now)
	if err != nil {
		return Account{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Account{}, err
	}
	return s.GetAccount(ctx, int(id))
}

func (s *GatewayStore) GetAccount(ctx context.Context, id int) (Account, error) {
	var a Account
	err := s.db.QueryRowContext(ctx, `
SELECT id, username, email, role, quota, used_quota, created_at, updated_at
FROM accounts WHERE id = ?`, id).Scan(&a.ID, &a.Username, &a.Email, &a.Role, &a.Quota,
		&a.UsedQuota, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (s *GatewayStore) Authenticate(ctx context.Context, username, password string) (Account, error) {
	var a Account
	var hash string
	err := s.db.QueryRowContext(ctx, `
SELECT id, username, email, role, quota, used_quota, created_at, updated_at, password_hash
FROM accounts WHERE username = ?`, strings.TrimSpace(username)).Scan(&a.ID, &a.Username, &a.Email,
		&a.Role, &a.Quota, &a.UsedQuota, &a.CreatedAt, &a.UpdatedAt, &hash)
	if err != nil || !passwordMatches(hash, password) {
		return Account{}, errors.New("invalid username or password")
	}
	return a, nil
}

func (s *GatewayStore) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, username, email, role, quota, used_quota, created_at, updated_at
FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Username, &a.Email, &a.Role, &a.Quota, &a.UsedQuota, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *GatewayStore) DeleteAccount(ctx context.Context, id int) error {
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM accounts WHERE id = ?`, id).Scan(&role); err != nil {
		return err
	}
	if role == "admin" {
		var admins int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE role = 'admin'`).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return errors.New("cannot delete the last admin account")
		}
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, id)
	return err
}

func (s *GatewayStore) ChangePassword(ctx context.Context, id int, current, next string) error {
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM accounts WHERE id = ?`, id).Scan(&hash); err != nil {
		return err
	}
	if !passwordMatches(hash, current) {
		return errors.New("current password is incorrect")
	}
	nextHash, err := passwordHash(next)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE accounts SET password_hash = ?, updated_at = ? WHERE id = ?`,
		nextHash, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (s *GatewayStore) IssueToken(ctx context.Context, accountID int, name, prefix string, ttl time.Duration) (APIToken, string, error) {
	raw, err := randomToken(prefix)
	if err != nil {
		return APIToken{}, "", err
	}
	id := newID("tok")
	now := time.Now().UTC()
	var expires any
	if ttl > 0 {
		expires = now.Add(ttl).Format(time.RFC3339Nano)
	}
	keyPrefix := raw
	if len(keyPrefix) > 12 {
		keyPrefix = keyPrefix[:12]
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO auth_tokens(id, account_id, name, token_hash, key_prefix, expires_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`, id, accountID, name, tokenDigest(raw), keyPrefix, expires,
		now.Format(time.RFC3339Nano))
	if err != nil {
		return APIToken{}, "", err
	}
	return APIToken{ID: id, Name: name, Key: keyPrefix + "...", Status: 1, UnlimitedQuota: true, CreatedAt: now.Format(time.RFC3339Nano)}, raw, nil
}

func (s *GatewayStore) AccountForToken(ctx context.Context, raw string) (Account, error) {
	var accountID int
	var expires sql.NullString
	err := s.db.QueryRowContext(ctx, `
SELECT account_id, expires_at FROM auth_tokens WHERE token_hash = ? AND status = 1`, tokenDigest(raw)).Scan(&accountID, &expires)
	if err != nil {
		return Account{}, err
	}
	if expires.Valid {
		deadline, err := time.Parse(time.RFC3339Nano, expires.String)
		if err != nil || time.Now().After(deadline) {
			return Account{}, errors.New("token expired")
		}
	}
	return s.GetAccount(ctx, accountID)
}

func (s *GatewayStore) RevokeToken(ctx context.Context, raw string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE auth_tokens SET status = 0 WHERE token_hash = ?`, tokenDigest(raw))
	return err
}

func (s *GatewayStore) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, key_prefix, status, unlimited_quota, created_at
FROM auth_tokens WHERE expires_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Key, &t.Status, &t.UnlimitedQuota, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Key += "..."
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *GatewayStore) SetTokenStatus(ctx context.Context, id string, status int) error {
	if status != 0 && status != 1 {
		return errors.New("status must be 0 or 1")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE auth_tokens SET status = ? WHERE id = ? AND expires_at IS NULL`, status, id)
	return err
}

func (s *GatewayStore) DeleteToken(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_tokens WHERE id = ? AND expires_at IS NULL`, id)
	return err
}
