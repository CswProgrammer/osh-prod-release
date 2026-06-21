package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type workflowRun struct {
	ID         int64
	Status     string
	Conclusion string
	HTMLURL    string
	CreatedAt  time.Time
}

type workflowRunsResponse struct {
	WorkflowRuns []struct {
		ID         int64   `json:"id"`
		Status     string  `json:"status"`
		Conclusion *string `json:"conclusion"`
		HTMLURL    string  `json:"html_url"`
		CreatedAt  string  `json:"created_at"`
		Event      string  `json:"event"`
	} `json:"workflow_runs"`
}

// WaitGreenWorkflows blocks until backend + frontend deploy-149.yml runs succeed.
func (d *DeployTrigger) WaitGreenWorkflows(ctx context.Context, since time.Time, maxWait time.Duration) (string, error) {
	return d.WaitSlotWorkflows(ctx, since, maxWait)
}

// WaitSlotWorkflows waits for latest workflow_dispatch runs after since (any slot).
func (d *DeployTrigger) WaitSlotWorkflows(ctx context.Context, since time.Time, maxWait time.Duration) (string, error) {
	return d.waitRepoWorkflows(ctx, since, maxWait)
}

func (d *DeployTrigger) waitRepoWorkflows(ctx context.Context, since time.Time, maxWait time.Duration) (string, error) {
	backend, err := d.backendTarget("")
	if err != nil {
		return "", err
	}
	frontend, err := d.frontendTarget("")
	if err != nil {
		return "", err
	}
	return d.waitRepoWorkflowsForTargets(ctx, []repoTarget{backend, frontend}, since, maxWait)
}

func (d *DeployTrigger) waitRepoWorkflowsForTargets(ctx context.Context, targets []repoTarget, since time.Time, maxWait time.Duration) (string, error) {
	if d.cfg.GitHubToken == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not configured")
	}
	deadline := time.Now().Add(maxWait)
	pending := make(map[string]bool, len(targets))
	for _, t := range targets {
		pending[t.repo] = true
	}

	var done []string
	poll := 10 * time.Second

	for time.Now().Before(deadline) {
		for _, t := range targets {
			if !pending[t.repo] {
				continue
			}
			run, err := d.fetchLatestDispatchRun(ctx, t.repo, since)
			if err != nil {
				return "", err
			}
			if run == nil {
				continue
			}
			switch run.Status {
			case "completed":
				if run.Conclusion != "success" {
					c := run.Conclusion
					if c == "" {
						c = "failed"
					}
					return "", fmt.Errorf("%s workflow 未成功（%s） %s", t.repo, c, run.HTMLURL)
				}
				delete(pending, t.repo)
				done = append(done, fmt.Sprintf("%s run#%d ok", t.repo, run.ID))
			case "queued", "in_progress", "waiting", "requested", "pending":
				// keep waiting
			default:
				// unknown status, keep waiting
			}
		}
		if len(pending) == 0 {
			return strings.Join(done, "; "), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(poll):
		}
	}

	var waiting []string
	for repo := range pending {
		waiting = append(waiting, repo)
	}
	return "", fmt.Errorf("等待 GitHub Actions 超时（%s），仍在进行: %s", maxWait, strings.Join(waiting, ", "))
}

func (d *DeployTrigger) fetchLatestDispatchRun(ctx context.Context, repo string, since time.Time) (*workflowRun, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo %q", repo)
	}
	url := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/actions/workflows/%s/runs?event=workflow_dispatch&per_page=15",
		parts[0], parts[1], slotWorkflowFile,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+d.cfg.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github list runs HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var body workflowRunsResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}

	var best *workflowRun
	for _, wr := range body.WorkflowRuns {
		if wr.Event != "workflow_dispatch" {
			continue
		}
		created, err := time.Parse(time.RFC3339, wr.CreatedAt)
		if err != nil {
			continue
		}
		if created.Before(since) {
			continue
		}
		conclusion := ""
		if wr.Conclusion != nil {
			conclusion = *wr.Conclusion
		}
		candidate := &workflowRun{
			ID: wr.ID, Status: wr.Status, Conclusion: conclusion,
			HTMLURL: wr.HTMLURL, CreatedAt: created,
		}
		if best == nil || candidate.CreatedAt.After(best.CreatedAt) {
			best = candidate
		}
	}
	return best, nil
}
