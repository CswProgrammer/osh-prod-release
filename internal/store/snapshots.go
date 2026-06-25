package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/juege/osh-prod-release/internal/models"
)

func (s *Store) SaveDeploySnapshot(ctx context.Context, snap models.DeploySnapshot) (*models.DeploySnapshot, error) {
	if snap.ID == "" {
		snap.ID = uuid.New().String()[:12]
	}
	if snap.Status == "" {
		snap.Status = "success"
	}
	now := time.Now().UTC()
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO deploy_snapshots (
			id, release_id, deploy_target, title, backend_git_ref, frontend_git_ref,
			backend_sha, frontend_sha, actor, status, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		snap.ID, optionalString(snap.ReleaseID), snap.DeployTarget, snap.Title,
		snap.BackendGitRef, snap.FrontendGitRef, optionalString(snap.BackendSHA), optionalString(snap.FrontendSHA),
		snap.Actor, snap.Status, snap.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	return s.GetDeploySnapshot(ctx, snap.ID)
}

func optionalString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func (s *Store) GetDeploySnapshot(ctx context.Context, id string) (*models.DeploySnapshot, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, release_id, deploy_target, title, backend_git_ref, frontend_git_ref,
			backend_sha, frontend_sha, actor, status, created_at
		FROM deploy_snapshots WHERE id = ?`, id)
	return scanDeploySnapshot(row)
}

func (s *Store) ListDeploySnapshots(ctx context.Context, target string, limit int) ([]models.DeploySnapshot, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `
		SELECT id, release_id, deploy_target, title, backend_git_ref, frontend_git_ref,
			backend_sha, frontend_sha, actor, status, created_at
		FROM deploy_snapshots`
	args := []any{}
	if target != "" {
		q += ` WHERE deploy_target = ?`
		args = append(args, target)
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.DeploySnapshot
	for rows.Next() {
		snap, err := scanDeploySnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *snap)
	}
	return out, rows.Err()
}

func (s *Store) GetPreviousDeploySnapshot(ctx context.Context, target string) (*models.DeploySnapshot, error) {
	list, err := s.ListDeploySnapshots(ctx, target, 2)
	if err != nil {
		return nil, err
	}
	if len(list) < 2 {
		return nil, fmt.Errorf("没有更早的成功部署版本可回滚")
	}
	return &list[1], nil
}

func scanDeploySnapshot(row interface{ Scan(...any) error }) (*models.DeploySnapshot, error) {
	var snap models.DeploySnapshot
	var releaseID, backendSHA, frontendSHA sql.NullString
	var createdAt string
	err := row.Scan(
		&snap.ID, &releaseID, &snap.DeployTarget, &snap.Title,
		&snap.BackendGitRef, &snap.FrontendGitRef, &backendSHA, &frontendSHA,
		&snap.Actor, &snap.Status, &createdAt)
	if err != nil {
		return nil, err
	}
	if releaseID.Valid {
		snap.ReleaseID = releaseID.String
	}
	if backendSHA.Valid {
		snap.BackendSHA = backendSHA.String
	}
	if frontendSHA.Valid {
		snap.FrontendSHA = frontendSHA.String
	}
	snap.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &snap, nil
}
