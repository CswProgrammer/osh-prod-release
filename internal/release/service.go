package release

import (
	"context"
	"fmt"
	"sync"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/juege/osh-prod-release/internal/approval"
	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/github"
	"github.com/juege/osh-prod-release/internal/models"
	"github.com/juege/osh-prod-release/internal/ssh"
	"github.com/juege/osh-prod-release/internal/store"
	"github.com/juege/osh-prod-release/internal/testrunner"
	"github.com/juege/osh-prod-release/internal/traffic"
)

type Service struct {
	cfg      *config.Config
	store    *store.Store
	approval *approval.Engine
	ssh      *ssh.Client
	test     *testrunner.Runner
	artifact *github.ArtifactService
	deployGH *github.DeployTrigger
	traffic  *traffic.Service
	mu       sync.Mutex
}

func New(cfg *config.Config, st *store.Store, trafficSvc *traffic.Service) *Service {
	return &Service{
		cfg:      cfg,
		store:    st,
		approval: approval.New(cfg.BossReviewer),
		ssh:      ssh.New(cfg),
		test:     testrunner.New(cfg),
		artifact: github.New(cfg),
		deployGH: github.NewDeployTrigger(cfg),
		traffic:  trafficSvc,
	}
}

func (s *Service) Store() *store.Store { return s.store }

func (s *Service) Create(ctx context.Context, req models.CreateReleaseRequest) (*models.Release, error) {
	if req.Title == "" || req.CommitSHA == "" || req.Author == "" {
		return nil, fmt.Errorf("title, commit_sha, author required")
	}
	if req.Level == "" {
		req.Level = models.LevelNormal
	}
	if _, err := s.artifact.Resolve(ctx, req.Repo, req.CommitSHA); err != nil {
		return nil, err
	}
	rel, err := s.store.CreateRelease(ctx, req)
	if err != nil {
		return nil, err
	}
	_ = s.store.AddAudit(ctx, req.Author, "create_release", rel.ID, rel.Title)
	return rel, nil
}

func (s *Service) ApplyAdminFastTrack(ctx context.Context, id, actor string) (*models.Release, error) {
	if err := s.store.SetBossApproved(ctx, id, actor); err != nil {
		return nil, err
	}
	_ = s.store.UpdateStep(ctx, id, "submit_review", "skipped", "管理员直通发布")
	_ = s.store.UpdateStep(ctx, id, "item_reviews", "skipped", "管理员直通发布")
	_ = s.store.UpdateStep(ctx, id, "boss_approve", "skipped", "管理员直通发布")
	if err := s.store.UpdateReleaseStatus(ctx, id, models.StatusApproved); err != nil {
		return nil, err
	}
	_ = s.store.AddAudit(ctx, actor, "admin_fast_track", id, "跳过双评审与终审")
	return s.store.GetRelease(ctx, id)
}

func (s *Service) Get(ctx context.Context, id string) (*models.Release, error) {
	return s.store.GetRelease(ctx, id)
}

func (s *Service) List(ctx context.Context) ([]models.Release, error) {
	return s.store.ListReleases(ctx)
}

func (s *Service) SubmitForReview(ctx context.Context, id, actor string) (*models.Release, error) {
	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	if rel.Status != models.StatusDraft {
		return nil, fmt.Errorf("only draft can submit review")
	}
	if err := s.store.UpdateReleaseStatus(ctx, id, models.StatusReviewing); err != nil {
		return nil, err
	}
	_ = s.store.UpdateStep(ctx, id, "submit_review", "success", "已提交评审")
	_ = s.store.AddAudit(ctx, actor, "submit_review", id, "")
	return s.store.GetRelease(ctx, id)
}

func (s *Service) SubmitReview(ctx context.Context, itemID string, req models.SubmitReviewRequest) (*models.Release, error) {
	item, err := s.store.GetItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	rel, err := s.store.GetRelease(ctx, item.ReleaseID)
	if err != nil {
		return nil, err
	}
	if rel.Status != models.StatusReviewing && rel.Status != models.StatusDraft {
		return nil, fmt.Errorf("release not in reviewing state")
	}
	if req.Reviewer != item.Reviewer1 && req.Reviewer != item.Reviewer2 {
		return nil, fmt.Errorf("reviewer not assigned to this item")
	}
	if req.Result == models.ReviewApprove && !req.Tested {
		return nil, fmt.Errorf("must confirm tested before approve")
	}
	if item.DemoRequired && req.Reviewer != item.Developer && req.Result == models.ReviewApprove && !req.DemoSeen {
		return nil, fmt.Errorf("must confirm demo seen")
	}
	if _, err := s.store.AddReview(ctx, itemID, req); err != nil {
		return nil, err
	}
	_ = s.store.AddAudit(ctx, req.Reviewer, "submit_item_review", itemID, string(req.Result))

	rel, _ = s.store.GetRelease(ctx, item.ReleaseID)
	ok, msg := s.approval.AllItemsReviewOK(rel.Items)
	if ok {
		_ = s.store.UpdateStep(ctx, rel.ID, "item_reviews", "success", "双评审通过")
		_ = s.store.MarkItemsApproved(ctx, rel.ID)
	} else {
		_ = s.store.UpdateStep(ctx, rel.ID, "item_reviews", "running", msg)
	}
	return s.store.GetRelease(ctx, item.ReleaseID)
}

func (s *Service) BossApprove(ctx context.Context, id string, req models.BossApproveRequest, actorIsBoss bool) (*models.Release, error) {
	if !actorIsBoss {
		boss := s.cfg.BossReviewer
		if boss == "" {
			boss = "juege"
		}
		return nil, fmt.Errorf("仅 %s 可终审通过", boss)
	}
	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Reviewer == "" {
		req.Reviewer = s.cfg.BossReviewer
	}
	ok, msg := s.approval.AllItemsReviewOK(rel.Items)
	if !ok {
		return nil, fmt.Errorf(msg)
	}
	if err := s.store.SetBossApproved(ctx, id, req.Reviewer); err != nil {
		return nil, err
	}
	_ = s.store.UpdateStep(ctx, id, "boss_approve", "success", req.Comment)
	_ = s.store.AddAudit(ctx, req.Reviewer, "boss_approve", id, req.Comment)
	return s.store.GetRelease(ctx, id)
}

func (s *Service) GetActiveDeploy(ctx context.Context) (*models.Release, error) {
	return s.store.GetActiveDeployingRelease(ctx)
}

// StartDeploy kicks off deploy in background and returns immediately (frontend polls status).
func (s *Service) StartDeploy(ctx context.Context, id, actor string, adminBypass bool) (*models.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	active, err := s.store.GetActiveDeployingRelease(ctx)
	if err != nil {
		return nil, err
	}
	if active != nil {
		if active.ID != id {
			return nil, fmt.Errorf("已有发布单「%s」正在部署中，请等待完成后再部署", active.Title)
		}
		return nil, fmt.Errorf("当前发布单正在部署中，请等待完成")
	}

	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	ok, msg := s.approval.CanStartDeploy(*rel, adminBypass)
	if !ok {
		return nil, fmt.Errorf(msg)
	}

	target := rel.DeployTarget
	if target == "" {
		target = "green"
	}
	if target == "blue" {
		if s.traffic == nil {
			return nil, fmt.Errorf("traffic service not configured")
		}
		if err := s.traffic.RequireProductionGreen(ctx); err != nil {
			return nil, err
		}
	}

	deployMsg := "审批已通过，准备部署到绿环境"
	if target == "blue" {
		deployMsg = "审批已通过，准备部署到蓝环境"
	}

	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusDeploying)
	_ = s.store.UpdateStep(ctx, id, "deploy_standby", "running", deployMsg)

	go s.executeDeploy(id, actor)
	return s.store.GetRelease(ctx, id)
}

func (s *Service) executeDeploy(id, actor string) {
	ctx := context.Background()

	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return
	}
	target := rel.DeployTarget
	if target == "" {
		target = "green"
	}
	if target == "blue" && s.traffic != nil {
		if err := s.traffic.RequireProductionGreen(ctx); err != nil {
			_ = s.store.UpdateStep(ctx, id, "deploy_standby", "failed", err.Error())
			_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
			return
		}
	}

	var out string
	var errDeploy error
	if target == "blue" {
		out, errDeploy = s.executeBlueDeploy(ctx, id)
	} else {
		out, errDeploy = s.executeGreenDeploy(ctx, id)
	}
	if errDeploy != nil {
		_ = s.store.UpdateStep(ctx, id, "deploy_standby", "failed", errDeploy.Error())
		_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
		return
	}
	_ = s.store.UpdateStep(ctx, id, "deploy_standby", "success", out)
	_ = s.store.AddAudit(ctx, actor, "deploy_standby", id, out)

	env := "green"
	if target == "blue" {
		env = "blue"
	}
	_, _ = s.runAutoTest(ctx, id, actor, env, target)
}

func (s *Service) executeGreenDeploy(ctx context.Context, id string) (string, error) {
	var out string
	var errDeploy error
	if s.cfg.GitHubToken != "" && (s.cfg.GitHubBackendRepo != "" || s.cfg.GitHubFrontendRepo != "") {
		dispatchSince := time.Now().UTC().Add(-15 * time.Second)
		out, errDeploy = s.deployGH.TriggerGreen149(ctx, "", "", id)
		if errDeploy == nil {
			_ = s.store.UpdateStep(ctx, id, "deploy_standby", "running", "GHA 已触发，等待前后端 workflow 跑完（约 3–8 分钟）…")
			ghaOut, waitErr := s.deployGH.WaitGreenWorkflows(ctx, dispatchSince, 25*time.Minute)
			if waitErr != nil {
				errDeploy = waitErr
			} else {
				out = out + "; GHA: " + ghaOut
				_ = s.store.UpdateStep(ctx, id, "deploy_standby", "running", "GHA 已完成，验证绿环境 HTTP…")
				waitOut, waitErr := s.ssh.WaitGreenAPI(ctx, "28080", 3*time.Minute)
				if waitErr != nil {
					errDeploy = waitErr
				} else {
					out = out + "; " + waitOut
				}
			}
		}
	} else {
		out, errDeploy = s.ssh.DeployGreenCode(ctx)
	}
	return out, errDeploy
}

func (s *Service) executeBlueDeploy(ctx context.Context, id string) (string, error) {
	var out string
	var errDeploy error
	if s.cfg.GitHubToken != "" && (s.cfg.GitHubBackendRepo != "" || s.cfg.GitHubFrontendRepo != "") {
		dispatchSince := time.Now().UTC().Add(-15 * time.Second)
		out, errDeploy = s.deployGH.TriggerSlot149(ctx, "", "", id, "blue")
		if errDeploy == nil {
			_ = s.store.UpdateStep(ctx, id, "deploy_standby", "running", "GHA 已触发，等待前后端 workflow 跑完（约 3–8 分钟）…")
			ghaOut, waitErr := s.deployGH.WaitSlotWorkflows(ctx, dispatchSince, 30*time.Minute)
			if waitErr != nil {
				errDeploy = waitErr
			} else {
				out = out + "; GHA: " + ghaOut
				_ = s.store.UpdateStep(ctx, id, "deploy_standby", "running", "GHA 已完成，验证蓝环境 :58080…")
				waitOut, waitErr := s.ssh.WaitSlotAPI(ctx, "blue", "58080", 5*time.Minute)
				if waitErr != nil {
					errDeploy = waitErr
				} else {
					out = out + "; " + waitOut
				}
			}
		}
	} else {
		out, errDeploy = s.ssh.DeployBlueCode(ctx)
	}
	return out, errDeploy
}

func (s *Service) runAutoTest(ctx context.Context, id, actor, env, target string) (*models.Release, error) {
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusTesting)
	_ = s.store.UpdateStep(ctx, id, "auto_test", "running", "")

	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	report, err := s.test.Run(ctx, *rel, env)
	if err != nil {
		_ = s.store.UpdateStep(ctx, id, "auto_test", "failed", err.Error())
		_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
		return nil, err
	}
	_ = s.store.SaveTestReport(ctx, *report)
	if !report.Passed {
		_ = s.store.UpdateStep(ctx, id, "auto_test", "failed", report.AIVerdict)
		_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
		return s.store.GetRelease(ctx, id)
	}
	_ = s.store.UpdateStep(ctx, id, "auto_test", "success", report.AIVerdict)
	_ = s.store.AddAudit(ctx, actor, "auto_test_pass", id, report.AIVerdict)
	if target == "blue" {
		return s.finishBlueDeploy(ctx, id, actor, report.AIVerdict)
	}
	return s.finishGreenDeploy(ctx, id, actor, report.AIVerdict)
}

// finishGreenDeploy marks a green-only pre-release deploy as done (no prod traffic switch).
func (s *Service) finishGreenDeploy(ctx context.Context, id, actor, msg string) (*models.Release, error) {
	_ = s.store.UpdateStep(ctx, id, "switch_traffic", "skipped", "绿环境预发，不切生产流量")
	_ = s.store.UpdateStep(ctx, id, "manual_verify", "skipped", "")
	_ = s.store.UpdateStep(ctx, id, "sync_standby", "skipped", "")
	_ = s.store.UpdateStep(ctx, id, "finish", "success", "绿环境部署完成")
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusDone)
	_ = s.store.AddAudit(ctx, actor, "green_deploy_done", id, msg)
	s.recordDeploySnapshot(ctx, id, actor, "green")
	return s.store.GetRelease(ctx, id)
}

// finishBlueDeploy marks a blue standby deploy as done (no prod traffic switch).
func (s *Service) finishBlueDeploy(ctx context.Context, id, actor, msg string) (*models.Release, error) {
	_ = s.store.UpdateStep(ctx, id, "switch_traffic", "skipped", "蓝环境待命，不切生产流量")
	_ = s.store.UpdateStep(ctx, id, "manual_verify", "skipped", "请验收 :58080")
	_ = s.store.UpdateStep(ctx, id, "sync_standby", "skipped", "数据同步请使用「同步数据库」")
	_ = s.store.UpdateStep(ctx, id, "finish", "success", "蓝环境部署完成")
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusDone)
	_ = s.store.AddAudit(ctx, actor, "blue_deploy_done", id, msg)
	s.recordDeploySnapshot(ctx, id, actor, "blue")
	return s.store.GetRelease(ctx, id)
}

func (s *Service) SwitchTraffic(ctx context.Context, id, actor string) (*models.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	if rel.Status != models.StatusTesting {
		return nil, fmt.Errorf("auto tests must pass before switch (status=%s)", rel.Status)
	}

	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusSwitching)
	_ = s.store.UpdateStep(ctx, id, "switch_traffic", "running", "")

	out, err := s.ssh.SwitchToGreen(ctx)
	if err != nil {
		_ = s.store.UpdateStep(ctx, id, "switch_traffic", "failed", err.Error())
		_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
		return nil, err
	}
	_ = s.store.SetActiveSlot(ctx, id, "green")
	_ = s.store.UpdateStep(ctx, id, "switch_traffic", "success", out)
	_ = s.store.AddSwitchEvent(ctx, models.SwitchEvent{
		ID: uuid.New().String()[:12], ReleaseID: id,
		FromSlot: "blue", ToSlot: "green", Actor: actor, Reason: "deploy switch",
		CreatedAt: time.Now().UTC(),
	})
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusVerifying)
	_ = s.store.UpdateStep(ctx, id, "manual_verify", "running", "等待功能负责人生产人工复测")
	_ = s.store.AddAudit(ctx, actor, "switch_traffic", id, out)
	return s.store.GetRelease(ctx, id)
}

func (s *Service) ConfirmManualVerify(ctx context.Context, id, actor string) (*models.Release, error) {
	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	if rel.Status != models.StatusVerifying {
		return nil, fmt.Errorf("not in verifying state")
	}
	_ = s.store.UpdateStep(ctx, id, "manual_verify", "success", "人工复测通过")
	return s.SyncToStandby(ctx, id, actor)
}

func (s *Service) SyncToStandby(ctx context.Context, id, actor string) (*models.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.store.GetRelease(ctx, id); err != nil {
		return nil, err
	}
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusSyncing)
	_ = s.store.UpdateStep(ctx, id, "sync_standby", "running", "同一 commit 同步到蓝槽位")

	var out string
	var errSync error
	if s.cfg.GitHubToken != "" && (s.cfg.GitHubBackendRepo != "" || s.cfg.GitHubFrontendRepo != "") {
		out, errSync = s.deployGH.TriggerSlot149(ctx, "", "", id, "blue")
		if errSync == nil {
			waitOut, waitErr := s.ssh.WaitSlotAPI(ctx, "blue", "58080", 25*time.Minute)
			if waitErr != nil {
				errSync = waitErr
			} else {
				out = out + "; " + waitOut
			}
		}
	} else {
		out, errSync = s.ssh.DeployBlueCode(ctx)
	}
	if errSync != nil {
		_ = s.store.UpdateStep(ctx, id, "sync_standby", "failed", errSync.Error())
		_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
		return nil, errSync
	}
	_ = s.store.UpdateStep(ctx, id, "sync_standby", "success", out)
	_ = s.store.UpdateStep(ctx, id, "finish", "success", "发布完成")
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusDone)
	_ = s.store.AddAudit(ctx, actor, "release_done", id, out)
	return s.store.GetRelease(ctx, id)
}

func (s *Service) Rollback(ctx context.Context, id string, req models.ActionRequest) (*models.Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	out, err := s.ssh.SwitchToBlue(ctx)
	if err != nil {
		return nil, err
	}
	_ = s.store.SetActiveSlot(ctx, id, "blue")
	_ = s.store.AddSwitchEvent(ctx, models.SwitchEvent{
		ID: uuid.New().String()[:12], ReleaseID: id,
		FromSlot: rel.ActiveSlot, ToSlot: "blue", Actor: req.Actor, Reason: req.Reason,
		CreatedAt: time.Now().UTC(),
	})
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusRolledBack)
	_ = s.store.AddAudit(ctx, req.Actor, "rollback", id, out+" "+req.Reason)
	return s.store.GetRelease(ctx, id)
}

func (s *Service) TrafficStatus(ctx context.Context) (string, error) {
	return s.ssh.TrafficStatus(ctx)
}

func (s *Service) ListDeploySnapshots(ctx context.Context, target string) ([]models.DeploySnapshot, error) {
	return s.store.ListDeploySnapshots(ctx, target, 20)
}

func (s *Service) recordDeploySnapshot(ctx context.Context, releaseID, actor, target string) {
	rel, err := s.store.GetRelease(ctx, releaseID)
	if err != nil {
		return
	}
	title := rel.Title
	if title == "" {
		title = releaseID
	}
	_, _ = s.store.SaveDeploySnapshot(ctx, models.DeploySnapshot{
		ReleaseID:      releaseID,
		DeployTarget:   target,
		Title:          title,
		BackendGitRef:  s.cfg.GitHubBackendGitRef,
		FrontendGitRef: s.cfg.GitHubFrontendGitRef,
		Actor:          actor,
		Status:         "success",
	})
}

func (s *Service) RollbackDeploy(ctx context.Context, req models.DeployRollbackRequest, adminBypass bool) (*models.DeploySnapshot, error) {
	if !adminBypass {
		return nil, fmt.Errorf("仅管理员可执行部署版本回滚")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	target := strings.ToLower(strings.TrimSpace(req.Target))
	if target == "" {
		target = "green"
	}
	if target != "green" && target != "blue" {
		return nil, fmt.Errorf("target must be green or blue")
	}
	if target == "blue" && s.traffic != nil {
		if err := s.traffic.RequireProductionGreen(ctx); err != nil {
			return nil, err
		}
	}

	var snap *models.DeploySnapshot
	var err error
	if req.SnapshotID != "" {
		snap, err = s.store.GetDeploySnapshot(ctx, req.SnapshotID)
	} else {
		snap, err = s.store.GetPreviousDeploySnapshot(ctx, target)
	}
	if err != nil {
		return nil, err
	}
	if snap.DeployTarget != target {
		return nil, fmt.Errorf("快照目标环境不匹配")
	}

	backendRef := snap.BackendGitRef
	frontendRef := snap.FrontendGitRef
	if backendRef == "" {
		backendRef = s.cfg.GitHubBackendGitRef
	}
	if frontendRef == "" {
		frontendRef = s.cfg.GitHubFrontendGitRef
	}

	dispatchSince := time.Now().UTC().Add(-15 * time.Second)
	out, errDeploy := s.deployGH.TriggerSlot149(ctx, backendRef, frontendRef, "rollback-"+snap.ID, target)
	if errDeploy != nil {
		return nil, errDeploy
	}
	ghaOut, waitErr := s.deployGH.WaitSlotWorkflows(ctx, dispatchSince, 30*time.Minute)
	if waitErr != nil {
		return nil, waitErr
	}
	port := "28080"
	if target == "blue" {
		port = "58080"
	}
	waitOut, waitErr := s.ssh.WaitSlotAPI(ctx, target, port, 5*time.Minute)
	if waitErr != nil {
		return nil, waitErr
	}

	actor := req.Actor
	if actor == "" {
		actor = "ops"
	}
	rolled, err := s.store.SaveDeploySnapshot(ctx, models.DeploySnapshot{
		ReleaseID:      snap.ReleaseID,
		DeployTarget:   target,
		Title:          "回滚→" + snap.Title,
		BackendGitRef:  backendRef,
		FrontendGitRef: frontendRef,
		BackendSHA:     snap.BackendSHA,
		FrontendSHA:    snap.FrontendSHA,
		Actor:          actor,
		Status:         "success",
	})
	if err != nil {
		return nil, err
	}
	detail := out + "; GHA: " + ghaOut + "; " + waitOut
	if req.Reason != "" {
		detail += "; reason: " + req.Reason
	}
	_ = s.store.AddAudit(ctx, actor, "deploy_rollback", rolled.ID, detail)
	return rolled, nil
}
