package blue

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/github"
	"github.com/juege/osh-prod-release/internal/ssh"
	"github.com/juege/osh-prod-release/internal/store"
	"github.com/juege/osh-prod-release/internal/traffic"
)

type JobKind string

const (
	JobDeploy JobKind = "deploy"
	JobSync   JobKind = "sync"
)

type JobStatus string

const (
	StatusRunning JobStatus = "running"
	StatusSuccess JobStatus = "success"
	StatusFailed  JobStatus = "failed"
)

type Job struct {
	ID        string    `json:"id"`
	Kind      JobKind   `json:"kind"`
	Status    JobStatus `json:"status"`
	Message   string    `json:"message,omitempty"`
	Output    string    `json:"output,omitempty"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
}

type ActiveResponse struct {
	Busy bool `json:"busy"`
	Job  *Job `json:"job,omitempty"`
}

func New(cfg *config.Config, st *store.Store, sshClient *ssh.Client, deployGH *github.DeployTrigger, trafficSvc *traffic.Service) *Service {
	return &Service{
		cfg:      cfg,
		store:    st,
		ssh:      sshClient,
		deployGH: deployGH,
		traffic:  trafficSvc,
	}
}

type Service struct {
	cfg      *config.Config
	store    *store.Store
	ssh      *ssh.Client
	deployGH *github.DeployTrigger
	traffic  *traffic.Service

	mu     sync.RWMutex
	active *Job
}

func (s *Service) RequireProductionGreen(ctx context.Context) error {
	return s.traffic.RequireProductionGreen(ctx)
}

func (s *Service) Active() ActiveResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil {
		return ActiveResponse{Busy: false}
	}
	j := *s.active
	return ActiveResponse{Busy: j.Status == StatusRunning, Job: &j}
}

func (s *Service) GetJob() *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil {
		return nil
	}
	j := *s.active
	return &j
}

func (s *Service) guard(ctx context.Context) error {
	if err := s.traffic.RequireProductionGreen(ctx); err != nil {
		return err
	}
	activeRel, err := s.store.GetActiveDeployingRelease(ctx)
	if err != nil {
		return err
	}
	if activeRel != nil {
		return fmt.Errorf("发布单「%s」正在部署中，请等待完成后再操作蓝环境", activeRel.Title)
	}
	s.mu.RLock()
	if s.active != nil && s.active.Status == StatusRunning {
		kind := "操作"
		switch s.active.Kind {
		case JobDeploy:
			kind = "蓝环境部署"
		case JobSync:
			kind = "蓝库同步"
		}
		s.mu.RUnlock()
		return fmt.Errorf("%s进行中，请等待完成", kind)
	}
	s.mu.RUnlock()
	return nil
}

func (s *Service) StartDeploy(ctx context.Context, actor, reason string) (*Job, error) {
	if err := s.guard(ctx); err != nil {
		return nil, err
	}
	if s.cfg.GitHubToken == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN 未配置，无法触发蓝环境部署")
	}
	job := s.beginJob(JobDeploy, actor, reason)
	go s.runDeploy(job.ID, actor, reason)
	return s.snapshotJob(job.ID), nil
}

func (s *Service) StartSync(ctx context.Context, actor, reason string) (*Job, error) {
	if err := s.guard(ctx); err != nil {
		return nil, err
	}
	job := s.beginJob(JobSync, actor, reason)
	go s.runSync(job.ID, actor, reason)
	return s.snapshotJob(job.ID), nil
}

func (s *Service) beginJob(kind JobKind, actor, reason string) *Job {
	job := &Job{
		ID:        uuid.New().String()[:12],
		Kind:      kind,
		Status:    StatusRunning,
		Message:   "已启动…",
		Actor:     actor,
		Reason:    reason,
		StartedAt: time.Now().UTC(),
	}
	s.mu.Lock()
	s.active = job
	s.mu.Unlock()
	return job
}

func (s *Service) finishJob(id string, status JobStatus, message, output string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.ID != id {
		return
	}
	s.active.Status = status
	s.active.Message = message
	s.active.Output = output
	s.active.EndedAt = time.Now().UTC()
}

func (s *Service) snapshotJob(id string) *Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.active == nil || s.active.ID != id {
		return nil
	}
	j := *s.active
	return &j
}

func (s *Service) runDeploy(jobID, actor, reason string) {
	ctx := context.Background()
	dispatchSince := time.Now().UTC().Add(-15 * time.Second)

	out, err := s.deployGH.TriggerSlot149(ctx, "", "", "blue-"+jobID, "blue")
	if err != nil {
		s.finishJob(jobID, StatusFailed, err.Error(), out)
		_ = s.store.AddAudit(ctx, actor, "blue_deploy_failed", jobID, err.Error())
		return
	}

	s.mu.Lock()
	if s.active != nil && s.active.ID == jobID {
		s.active.Message = "GHA 已触发，等待前后端 workflow 完成…"
	}
	s.mu.Unlock()

	ghaOut, waitErr := s.deployGH.WaitSlotWorkflows(ctx, dispatchSince, 30*time.Minute)
	if waitErr != nil {
		s.finishJob(jobID, StatusFailed, waitErr.Error(), out+"; "+ghaOut)
		_ = s.store.AddAudit(ctx, actor, "blue_deploy_failed", jobID, waitErr.Error())
		return
	}

	s.mu.Lock()
	if s.active != nil && s.active.ID == jobID {
		s.active.Message = "GHA 完成，验证蓝环境 :58080…"
	}
	s.mu.Unlock()

	waitOut, waitErr := s.ssh.WaitSlotAPI(ctx, "blue", "58080", 5*time.Minute)
	if waitErr != nil {
		s.finishJob(jobID, StatusFailed, waitErr.Error(), out+"; "+ghaOut+"; "+waitOut)
		_ = s.store.AddAudit(ctx, actor, "blue_deploy_failed", jobID, waitErr.Error())
		return
	}

	full := out + "; GHA: " + ghaOut + "; " + waitOut
	s.finishJob(jobID, StatusSuccess, "蓝环境代码部署完成，请验收 :58080", full)
	_ = s.store.AddAudit(ctx, actor, "blue_deploy_done", jobID, full)
	if reason != "" {
		_ = s.store.AddAudit(ctx, actor, "blue_deploy_reason", jobID, reason)
	}
}

func (s *Service) runSync(jobID, actor, reason string) {
	ctx := context.Background()
	s.mu.Lock()
	if s.active != nil && s.active.ID == jobID {
		s.active.Message = "正在执行绿→蓝数据同步（约 5–20 分钟）…"
	}
	s.mu.Unlock()

	out, err := s.ssh.SyncGreenToBlue(ctx)
	if err != nil {
		s.finishJob(jobID, StatusFailed, err.Error(), out)
		_ = s.store.AddAudit(ctx, actor, "blue_sync_failed", jobID, err.Error())
		return
	}

	s.finishJob(jobID, StatusSuccess, "绿→蓝数据同步完成", out)
	_ = s.store.AddAudit(ctx, actor, "blue_sync_done", jobID, out)
	if reason != "" {
		_ = s.store.AddAudit(ctx, actor, "blue_sync_reason", jobID, reason)
	}
}
