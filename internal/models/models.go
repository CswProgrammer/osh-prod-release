package models

import "time"

type ReleaseLevel string

const (
	LevelNormal ReleaseLevel = "normal"
	LevelUrgent ReleaseLevel = "urgent"
)

type ReleaseStatus string

const (
	StatusDraft       ReleaseStatus = "draft"
	StatusReviewing   ReleaseStatus = "reviewing"
	StatusApproved    ReleaseStatus = "approved"
	StatusDeploying   ReleaseStatus = "deploying"
	StatusTesting     ReleaseStatus = "testing"
	StatusSwitching   ReleaseStatus = "switching"
	StatusVerifying   ReleaseStatus = "verifying"
	StatusSyncing     ReleaseStatus = "syncing"
	StatusDone        ReleaseStatus = "done"
	StatusRolledBack  ReleaseStatus = "rolledback"
	StatusFailed      ReleaseStatus = "failed"
)

type ChangeItemType string

const (
	ItemTypeCode      ChangeItemType = "code"
	ItemTypeMigration ChangeItemType = "migration"
)

type ChangeItemStatus string

const (
	ItemStatusPending   ChangeItemStatus = "pending"
	ItemStatusApproved  ChangeItemStatus = "approved"
	ItemStatusRejected  ChangeItemStatus = "rejected"
	ItemStatusDeployed  ChangeItemStatus = "deployed"
	ItemStatusVerified  ChangeItemStatus = "verified"
)

type ReviewResult string

const (
	ReviewApprove ReviewResult = "approve"
	ReviewReject  ReviewResult = "reject"
)

type Release struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Level       ReleaseLevel  `json:"level"`
	Repo        string        `json:"repo"`
	CommitSHA   string        `json:"commit_sha"`
	Status      ReleaseStatus `json:"status"`
	Author      string        `json:"author"`
	BossApproved bool         `json:"boss_approved"`
	BossApprovedBy string     `json:"boss_approved_by,omitempty"`
	BossApprovedAt *time.Time `json:"boss_approved_at,omitempty"`
	ActiveSlot   string        `json:"active_slot,omitempty"` // blue|green after switch
	DeployTarget string        `json:"deploy_target,omitempty"` // green|blue
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Items       []ChangeItem  `json:"items,omitempty"`
	Steps       []ReleaseStep `json:"steps,omitempty"`
}

type ChangeItem struct {
	ID           string           `json:"id"`
	ReleaseID    string           `json:"release_id"`
	Title        string           `json:"title"`
	Type         ChangeItemType   `json:"type"`
	Ref          string           `json:"ref"`
	Developer    string           `json:"developer"`
	ExpectedImpact string         `json:"expected_impact,omitempty"`
	Status       ChangeItemStatus `json:"status"`
	Reviewer1    string           `json:"reviewer1"`
	Reviewer2    string           `json:"reviewer2"`
	DemoRequired bool             `json:"demo_required"`
	Reviews      []Review         `json:"reviews,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
}

type Review struct {
	ID        string       `json:"id"`
	ItemID    string       `json:"item_id"`
	Reviewer  string       `json:"reviewer"`
	Tested    bool         `json:"tested"`
	DemoSeen  bool         `json:"demo_seen"`
	Result    ReviewResult `json:"result"`
	Comment   string       `json:"comment,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

type ReleaseStep struct {
	ID         string    `json:"id"`
	ReleaseID  string    `json:"release_id"`
	StepKey    string    `json:"step_key"`
	Title      string    `json:"title"`
	Status     string    `json:"status"` // pending|running|success|failed|skipped
	Message    string    `json:"message,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type TestReport struct {
	ID          string    `json:"id"`
	ReleaseID   string    `json:"release_id"`
	Env         string    `json:"env"`
	Functional  string    `json:"functional_json"`
	DataDiff    string    `json:"data_diff_json"`
	AIVerdict   string    `json:"ai_verdict"`
	AIPassed    bool      `json:"ai_passed"`
	Passed      bool      `json:"passed"`
	CreatedAt   time.Time `json:"created_at"`
}

type SwitchEvent struct {
	ID        string    `json:"id"`
	ReleaseID string    `json:"release_id"`
	FromSlot  string    `json:"from_slot"`
	ToSlot    string    `json:"to_slot"`
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

type AuditLog struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateReleaseRequest is the API payload for new release.
type CreateReleaseRequest struct {
	Title     string                   `json:"title"`
	Level     ReleaseLevel             `json:"level"`
	Repo      string                   `json:"repo"`
	CommitSHA string                   `json:"commit_sha"`
	Author       string                   `json:"author"`
	DeployTarget string                   `json:"deploy_target,omitempty"` // green|blue, default green
	Items     []CreateChangeItemRequest `json:"items"`
}

type CreateChangeItemRequest struct {
	Title          string         `json:"title"`
	Type           ChangeItemType `json:"type"`
	Ref            string         `json:"ref"`
	Developer      string         `json:"developer"`
	ExpectedImpact string         `json:"expected_impact"`
	Reviewer1      string         `json:"reviewer1"`
	Reviewer2      string         `json:"reviewer2"`
}

type SubmitReviewRequest struct {
	Reviewer string       `json:"reviewer"`
	Tested   bool         `json:"tested"`
	DemoSeen bool         `json:"demo_seen"`
	Result   ReviewResult `json:"result"`
	Comment  string       `json:"comment"`
}

type BossApproveRequest struct {
	Reviewer string `json:"reviewer"`
	Comment  string `json:"comment"`
}

type ActionRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason,omitempty"`
}

type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleNormal UserRole = "normal"
)

type User struct {
	Username    string   `json:"username"`
	Role        UserRole `json:"role"`
	DisplayName string   `json:"display_name"`
	IsAdmin     bool     `json:"is_admin"`
	IsBoss      bool     `json:"is_boss"`
}

type UserRecord struct {
	Username     string
	PasswordHash string
	Role         UserRole
	DisplayName  string
	CreatedAt    time.Time
}

func (u UserRecord) Public() User {
	return User{
		Username:    u.Username,
		Role:        u.Role,
		DisplayName: u.DisplayName,
		IsAdmin:     u.Role == RoleAdmin,
		IsBoss:      u.Role == RoleAdmin,
	}
}

type Session struct {
	Token     string
	Username  string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type DeploySnapshot struct {
	ID             string    `json:"id"`
	ReleaseID      string    `json:"release_id,omitempty"`
	DeployTarget   string    `json:"deploy_target"`
	Title          string    `json:"title"`
	BackendGitRef  string    `json:"backend_git_ref"`
	FrontendGitRef string    `json:"frontend_git_ref"`
	BackendSHA     string    `json:"backend_sha,omitempty"`
	FrontendSHA    string    `json:"frontend_sha,omitempty"`
	Actor          string    `json:"actor"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type DeployRollbackRequest struct {
	Target     string `json:"target"` // green|blue
	SnapshotID string `json:"snapshot_id,omitempty"`
	ToPrevious bool   `json:"to_previous"`
	Actor      string `json:"actor"`
	Reason     string `json:"reason,omitempty"`
}
