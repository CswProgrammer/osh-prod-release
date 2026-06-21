package traffic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/juege/osh-prod-release/internal/models"
	"github.com/juege/osh-prod-release/internal/ssh"
	"github.com/juege/osh-prod-release/internal/store"
)

type Service struct {
	store *store.Store
	ssh   *ssh.Client
}

func New(st *store.Store, sshClient *ssh.Client) *Service {
	return &Service{store: st, ssh: sshClient}
}

type Status struct {
	Active string `json:"active"` // blue | green | unknown
	Raw    string `json:"raw"`
}

func ParseActive(output string) string {
	const marker = "active (by :80):"
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, marker)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(line[idx+len(marker):])
		if paren := strings.Index(rest, "("); paren > 0 {
			rest = strings.TrimSpace(rest[:paren])
		}
		switch rest {
		case "blue", "green":
			return rest
		}
	}
	return "unknown"
}

func (s *Service) Status(ctx context.Context) (*Status, error) {
	raw, err := s.ssh.TrafficStatus(ctx)
	if err != nil {
		return nil, err
	}
	return &Status{Active: ParseActive(raw), Raw: raw}, nil
}

func (s *Service) RequireProductionGreen(ctx context.Context) error {
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if st.Active != "green" {
		label := "未知"
		if st.Active == "blue" {
			label = "蓝"
		} else if st.Active == "unknown" {
			label = "未知"
		} else {
			label = st.Active
		}
		return fmt.Errorf("当前生产流量在%s环境（:80），仅当生产在绿环境时才能操作蓝环境", label)
	}
	return nil
}

func (s *Service) guardNoActiveDeploy(ctx context.Context) error {
	active, err := s.store.GetActiveDeployingRelease(ctx)
	if err != nil {
		return err
	}
	if active != nil {
		return fmt.Errorf("发布单「%s」正在部署中，请等待完成后再切流", active.Title)
	}
	return nil
}

func (s *Service) SwitchToGreen(ctx context.Context, actor, reason string) (*Status, error) {
	if err := s.guardNoActiveDeploy(ctx); err != nil {
		return nil, err
	}
	st, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	if st.Active == "green" {
		return st, fmt.Errorf("当前生产流量已在绿环境，无需重复切换")
	}

	out, err := s.ssh.SwitchToGreen(ctx)
	if err != nil {
		if strings.TrimSpace(out) != "" {
			return nil, fmt.Errorf("%s", out)
		}
		return nil, err
	}
	from := st.Active
	if from == "" || from == "unknown" {
		from = "blue"
	}
	_ = s.store.AddSwitchEvent(ctx, models.SwitchEvent{
		ID: uuid.New().String()[:12], ReleaseID: "manual",
		FromSlot: from, ToSlot: "green", Actor: actor, Reason: reason,
		CreatedAt: time.Now().UTC(),
	})
	_ = s.store.AddAudit(ctx, actor, "traffic_to_green", "production", out)

	newSt, err := s.Status(ctx)
	if err != nil {
		return &Status{Active: "green", Raw: out}, nil
	}
	return newSt, nil
}

func (s *Service) SwitchToBlue(ctx context.Context, actor, reason string) (*Status, error) {
	if err := s.guardNoActiveDeploy(ctx); err != nil {
		return nil, err
	}
	st, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	if st.Active == "blue" {
		return st, fmt.Errorf("当前生产流量已在蓝环境，无需重复切换")
	}

	out, err := s.ssh.SwitchToBlue(ctx)
	if err != nil {
		if strings.TrimSpace(out) != "" {
			return nil, fmt.Errorf("%s", out)
		}
		return nil, err
	}
	from := st.Active
	if from == "" || from == "unknown" {
		from = "green"
	}
	_ = s.store.AddSwitchEvent(ctx, models.SwitchEvent{
		ID: uuid.New().String()[:12], ReleaseID: "manual",
		FromSlot: from, ToSlot: "blue", Actor: actor, Reason: reason,
		CreatedAt: time.Now().UTC(),
	})
	_ = s.store.AddAudit(ctx, actor, "traffic_to_blue", "production", out)

	newSt, err := s.Status(ctx)
	if err != nil {
		return &Status{Active: "blue", Raw: out}, nil
	}
	return newSt, nil
}

func (s *Service) History(ctx context.Context, limit int) ([]models.SwitchEvent, error) {
	return s.store.ListSwitchEvents(ctx, limit)
}
