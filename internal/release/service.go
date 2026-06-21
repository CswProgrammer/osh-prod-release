package release

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/juege/osh-prod-release/internal/approval"
	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/github"
	"github.com/juege/osh-prod-release/internal/models"
	"github.com/juege/osh-prod-release/internal/ssh"
	"github.com/juege/osh-prod-release/internal/store"
	"github.com/juege/osh-prod-release/internal/testrunner"
)

type Service struct {
	cfg      *config.Config
	store    *store.Store
	approval *approval.Engine
	ssh      *ssh.Client
	test     *testrunner.Runner
	artifact *github.ArtifactService
	deployGH *github.DeployTrigger
	mu       sync.Mutex
}

func New(cfg *config.Config, st *store.Store) *Service {
	return &Service{
		cfg:      cfg,
		store:    st,
		approval: approval.New(cfg.BossReviewer),
		ssh:      ssh.New(cfg),
		test:     testrunner.New(cfg),
		artifact: github.New(cfg),
		deployGH: github.NewDeployTrigger(cfg),
	}
}

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

func (s *Service) BossApprove(ctx context.Context, id string, req models.BossApproveRequest) (*models.Release, error) {
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
func (s *Service) StartDeploy(ctx context.Context, id, actor string) (*models.Release, error) {
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
	ok, msg := s.approval.CanStartDeploy(*rel)
	if !ok {
		return nil, fmt.Errorf(msg)
	}

	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusDeploying)
	_ = s.store.UpdateStep(ctx, id, "deploy_standby", "running", "审批已通过，准备部署到绿环境")

	go s.executeDeploy(id, actor)
	return s.store.GetRelease(ctx, id)
}

func (s *Service) executeDeploy(id, actor string) {
	ctx := context.Background()

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
	if errDeploy != nil {
		_ = s.store.UpdateStep(ctx, id, "deploy_standby", "failed", errDeploy.Error())
		_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusFailed)
		return
	}
	_ = s.store.UpdateStep(ctx, id, "deploy_standby", "success", out)
	_ = s.store.AddAudit(ctx, actor, "deploy_standby", id, out)

	_, _ = s.runAutoTest(ctx, id, actor)
}

func (s *Service) runAutoTest(ctx context.Context, id, actor string) (*models.Release, error) {
	_ = s.store.UpdateReleaseStatus(ctx, id, models.StatusTesting)
	_ = s.store.UpdateStep(ctx, id, "auto_test", "running", "")

	rel, err := s.store.GetRelease(ctx, id)
	if err != nil {
		return nil, err
	}
	report, err := s.test.Run(ctx, *rel, "green")
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
