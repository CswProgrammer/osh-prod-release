package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/juege/osh-prod-release/internal/models"
)

func (s *Store) EnsureUser(ctx context.Context, username, passwordHash string, role models.UserRole, displayName string) error {
	if displayName == "" {
		displayName = username
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, role, display_name, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET
			password_hash=excluded.password_hash,
			role=excluded.role,
			display_name=excluded.display_name`,
		username, passwordHash, string(role), displayName, now)
	return err
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*models.UserRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT username, password_hash, role, display_name, created_at
		FROM users WHERE username = ?`, username)
	var u models.UserRecord
	var roleStr string
	var createdAt string
	err := row.Scan(&u.Username, &u.PasswordHash, &roleStr, &u.DisplayName, &createdAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user not found")
	}
	if err != nil {
		return nil, err
	}
	u.Role = models.UserRole(roleStr)
	u.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &u, nil
}

func (s *Store) CreateSession(ctx context.Context, token, username string, expires time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token, username, expires_at, created_at)
		VALUES (?, ?, ?, ?)`,
		token, username, expires.UTC().Format(time.RFC3339), now)
	return err
}

func (s *Store) GetSession(ctx context.Context, token string) (*models.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT token, username, expires_at, created_at FROM sessions WHERE token = ?`, token)
	var sess models.Session
	var expiresAt, createdAt string
	if err := row.Scan(&sess.Token, &sess.Username, &expiresAt, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session expired or invalid")
		}
		return nil, err
	}
	exp, _ := time.Parse(time.RFC3339, expiresAt)
	if time.Now().UTC().After(exp) {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
		return nil, fmt.Errorf("session expired")
	}
	sess.ExpiresAt = exp
	sess.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &sess, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}
