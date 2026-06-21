package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/juege/osh-prod-release/internal/models"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaFS embed.FS

type Store struct {
	db *sql.DB
}

func Open(sqlitePath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(sqlitePath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqlitePath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(string(schema)); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateRelease(ctx context.Context, req models.CreateReleaseRequest) (*models.Release, error) {
	now := time.Now().UTC()
	id := uuid.New().String()[:12]
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO releases (id, title, level, repo, commit_sha, status, author, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, req.Title, req.Level, req.Repo, req.CommitSHA, models.StatusDraft, req.Author,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	for _, it := range req.Items {
		itemID := uuid.New().String()[:12]
		demoRequired := it.Developer == it.Reviewer1 || it.Developer == it.Reviewer2
		_, err = tx.ExecContext(ctx, `
			INSERT INTO change_items (id, release_id, title, type, ref, developer, expected_impact,
				status, reviewer1, reviewer2, demo_required, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			itemID, id, it.Title, it.Type, it.Ref, it.Developer, it.ExpectedImpact,
			models.ItemStatusPending, it.Reviewer1, it.Reviewer2, boolToInt(demoRequired),
			now.Format(time.RFC3339))
		if err != nil {
			return nil, err
		}
	}

	if err := s.initStepsTx(ctx, tx, id); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRelease(ctx, id)
}

func (s *Store) initStepsTx(ctx context.Context, tx *sql.Tx, releaseID string) error {
	steps := defaultReleaseSteps()
	for _, st := range steps {
		id := uuid.New().String()[:12]
		_, err := tx.ExecContext(ctx, `
			INSERT INTO release_steps (id, release_id, step_key, title, status)
			VALUES (?, ?, ?, ?, 'pending')`, id, releaseID, st.key, st.title)
		if err != nil {
			return err
		}
	}
	return nil
}

type stepDef struct {
	key   string
	title string
}

func defaultReleaseSteps() []stepDef {
	return []stepDef{
		{"submit_review", "提交评审"},
		{"item_reviews", "上线项双评审"},
		{"boss_approve", "觉哥终审"},
		{"deploy_standby", "部署到待命槽位"},
		{"auto_test", "自动化测试"},
		{"switch_traffic", "切流到新生产"},
		{"manual_verify", "生产人工复测"},
		{"sync_standby", "同步到另一槽位"},
		{"finish", "发布完成"},
	}
}

func (s *Store) GetRelease(ctx context.Context, id string) (*models.Release, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, title, level, repo, commit_sha, status, author,
			boss_approved, boss_approved_by, boss_approved_at, active_slot, created_at, updated_at
		FROM releases WHERE id = ?`, id)
	r, err := scanRelease(row)
	if err != nil {
		return nil, err
	}
	items, err := s.listItems(ctx, id)
	if err != nil {
		return nil, err
	}
	r.Items = items
	steps, err := s.listSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	r.Steps = steps
	return r, nil
}

func (s *Store) ListReleases(ctx context.Context) ([]models.Release, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, title, level, repo, commit_sha, status, author,
			boss_approved, boss_approved_by, boss_approved_at, active_slot, created_at, updated_at
		FROM releases ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Release
	for rows.Next() {
		r, err := scanRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func scanRelease(row interface{ Scan(...any) error }) (*models.Release, error) {
	var r models.Release
	var bossApproved int
	var bossBy, bossAt, activeSlot sql.NullString
	var createdAt, updatedAt string
	err := row.Scan(&r.ID, &r.Title, &r.Level, &r.Repo, &r.CommitSHA, &r.Status, &r.Author,
		&bossApproved, &bossBy, &bossAt, &activeSlot, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	r.BossApproved = bossApproved == 1
	if bossBy.Valid {
		r.BossApprovedBy = bossBy.String
	}
	if bossAt.Valid {
		t, _ := time.Parse(time.RFC3339, bossAt.String)
		r.BossApprovedAt = &t
	}
	if activeSlot.Valid {
		r.ActiveSlot = activeSlot.String
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return &r, nil
}

func (s *Store) listItems(ctx context.Context, releaseID string) ([]models.ChangeItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, release_id, title, type, ref, developer, expected_impact, status,
			reviewer1, reviewer2, demo_required, created_at
		FROM change_items WHERE release_id = ? ORDER BY created_at`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []models.ChangeItem
	for rows.Next() {
		var it models.ChangeItem
		var demo int
		var created string
		if err := rows.Scan(&it.ID, &it.ReleaseID, &it.Title, &it.Type, &it.Ref, &it.Developer,
			&it.ExpectedImpact, &it.Status, &it.Reviewer1, &it.Reviewer2, &demo, &created); err != nil {
			return nil, err
		}
		it.DemoRequired = demo == 1
		it.CreatedAt, _ = time.Parse(time.RFC3339, created)
		reviews, err := s.listReviews(ctx, it.ID)
		if err != nil {
			return nil, err
		}
		it.Reviews = reviews
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *Store) GetItem(ctx context.Context, itemID string) (*models.ChangeItem, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, release_id, title, type, ref, developer, expected_impact, status,
			reviewer1, reviewer2, demo_required, created_at
		FROM change_items WHERE id = ?`, itemID)
	var it models.ChangeItem
	var demo int
	var created string
	if err := row.Scan(&it.ID, &it.ReleaseID, &it.Title, &it.Type, &it.Ref, &it.Developer,
		&it.ExpectedImpact, &it.Status, &it.Reviewer1, &it.Reviewer2, &demo, &created); err != nil {
		return nil, err
	}
	it.DemoRequired = demo == 1
	it.CreatedAt, _ = time.Parse(time.RFC3339, created)
	reviews, err := s.listReviews(ctx, it.ID)
	if err != nil {
		return nil, err
	}
	it.Reviews = reviews
	return &it, nil
}

func (s *Store) listReviews(ctx context.Context, itemID string) ([]models.Review, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, item_id, reviewer, tested, demo_seen, result, comment, created_at
		FROM reviews WHERE item_id = ? ORDER BY created_at`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Review
	for rows.Next() {
		var rv models.Review
		var tested, demoSeen int
		var created string
		if err := rows.Scan(&rv.ID, &rv.ItemID, &rv.Reviewer, &tested, &demoSeen, &rv.Result, &rv.Comment, &created); err != nil {
			return nil, err
		}
		rv.Tested = tested == 1
		rv.DemoSeen = demoSeen == 1
		rv.CreatedAt, _ = time.Parse(time.RFC3339, created)
		out = append(out, rv)
	}
	return out, rows.Err()
}

func (s *Store) AddReview(ctx context.Context, itemID string, req models.SubmitReviewRequest) (*models.Review, error) {
	id := uuid.New().String()[:12]
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reviews (id, item_id, reviewer, tested, demo_seen, result, comment, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, itemID, req.Reviewer, boolToInt(req.Tested), boolToInt(req.DemoSeen), req.Result, req.Comment, now)
	if err != nil {
		return nil, err
	}
	return &models.Review{
		ID: id, ItemID: itemID, Reviewer: req.Reviewer, Tested: req.Tested,
		DemoSeen: req.DemoSeen, Result: req.Result, Comment: req.Comment,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (s *Store) UpdateReleaseStatus(ctx context.Context, id string, status models.ReleaseStatus) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE releases SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) SetBossApproved(ctx context.Context, id, reviewer string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		UPDATE releases SET boss_approved = 1, boss_approved_by = ?, boss_approved_at = ?,
			status = ?, updated_at = ? WHERE id = ?`,
		reviewer, now, models.StatusApproved, now, id)
	return err
}

func (s *Store) SetActiveSlot(ctx context.Context, id, slot string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE releases SET active_slot = ?, updated_at = ? WHERE id = ?`,
		slot, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (s *Store) UpdateItemStatus(ctx context.Context, itemID string, status models.ChangeItemStatus) error {
	_, err := s.db.ExecContext(ctx, `UPDATE change_items SET status = ? WHERE id = ?`, status, itemID)
	return err
}

func (s *Store) UpdateStep(ctx context.Context, releaseID, stepKey, status, message string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var started, finished sql.NullString
	if status == "running" {
		started = sql.NullString{String: now, Valid: true}
	}
	if status == "success" || status == "failed" || status == "skipped" {
		finished = sql.NullString{String: now, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE release_steps SET status = ?, message = ?,
			started_at = COALESCE(started_at, ?),
			finished_at = CASE WHEN ? != '' THEN ? ELSE finished_at END
		WHERE release_id = ? AND step_key = ?`,
		status, message, nullStr(started), nullStr(finished), nullStr(finished), releaseID, stepKey)
	return err
}

func nullStr(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func (s *Store) listSteps(ctx context.Context, releaseID string) ([]models.ReleaseStep, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, release_id, step_key, title, status, message, started_at, finished_at
		FROM release_steps WHERE release_id = ? ORDER BY rowid`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ReleaseStep
	for rows.Next() {
		var st models.ReleaseStep
		var msg, started, finished sql.NullString
		if err := rows.Scan(&st.ID, &st.ReleaseID, &st.StepKey, &st.Title, &st.Status, &msg, &started, &finished); err != nil {
			return nil, err
		}
		st.Message = msg.String
		if started.Valid {
			t, _ := time.Parse(time.RFC3339, started.String)
			st.StartedAt = &t
		}
		if finished.Valid {
			t, _ := time.Parse(time.RFC3339, finished.String)
			st.FinishedAt = &t
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) SaveTestReport(ctx context.Context, r models.TestReport) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO test_reports (id, release_id, env, functional_json, data_diff_json,
			ai_verdict, ai_passed, passed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ReleaseID, r.Env, r.Functional, r.DataDiff, r.AIVerdict,
		boolToInt(r.AIPassed), boolToInt(r.Passed), r.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) AddSwitchEvent(ctx context.Context, e models.SwitchEvent) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO switch_events (id, release_id, from_slot, to_slot, reason, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.ReleaseID, e.FromSlot, e.ToSlot, e.Reason, e.Actor, e.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) AddAudit(ctx context.Context, actor, action, target, detail string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_logs (id, actor, action, target, detail, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.New().String()[:12], actor, action, target, detail, time.Now().UTC().Format(time.RFC3339))
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) GetActiveDeployingRelease(ctx context.Context) (*models.Release, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT r.id FROM releases r
		WHERE r.status = 'deploying'
		   OR r.status IN ('switching', 'verifying', 'syncing')
		   OR (
		     r.status = 'testing'
		     AND EXISTS (
		       SELECT 1 FROM release_steps
		       WHERE release_id = r.id AND step_key = 'auto_test'
		         AND status IN ('pending', 'running')
		     )
		   )
		   OR (
		     r.status = 'testing'
		     AND EXISTS (
		       SELECT 1 FROM release_steps
		       WHERE release_id = r.id AND step_key = 'deploy_standby'
		         AND status = 'running'
		     )
		   )
		ORDER BY r.updated_at DESC LIMIT 1`)
	var id string
	if err := row.Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return s.GetRelease(ctx, id)
}

func (s *Store) MarkItemsApproved(ctx context.Context, releaseID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE change_items SET status = ? WHERE release_id = ? AND status = ?`,
		models.ItemStatusApproved, releaseID, models.ItemStatusPending)
	return err
}

func ErrNotFound(msg string) error {
	return fmt.Errorf("not found: %s", msg)
}
