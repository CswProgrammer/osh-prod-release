package ssh

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/juege/osh-prod-release/internal/config"
)

type Client struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Client {
	return &Client{cfg: cfg}
}

func (c *Client) Run(ctx context.Context, remoteCmd string, timeout time.Duration) (string, error) {
	if c.cfg.MockMode {
		return fmt.Sprintf("[MOCK] %s", remoteCmd), nil
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if useLocalExec(c.cfg) {
		cmd := exec.CommandContext(ctx, "bash", "-lc", remoteCmd)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	if c.cfg.ProdSSHPassword == "" {
		return "", fmt.Errorf("PROD_PASSWORD required for remote SSH mode (set PROD_EXEC_MODE=local on prod host or configure password)")
	}
	args := []string{
		"-p", c.cfg.ProdSSHPassword,
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=15",
		"-p", fmt.Sprintf("%d", c.cfg.ProdSSHPort),
		fmt.Sprintf("%s@%s", c.cfg.ProdSSHUser, c.cfg.ProdSSHHost),
		remoteCmd,
	}
	cmd := exec.CommandContext(ctx, "sshpass", args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (c *Client) SwitchToGreen(ctx context.Context) (string, error) {
	script := c.cfg.TrafficSwitchScript
	return c.Run(ctx, fmt.Sprintf("bash %s to-green", script), 3*time.Minute)
}

func (c *Client) SwitchToBlue(ctx context.Context) (string, error) {
	script := c.cfg.TrafficSwitchScript
	return c.Run(ctx, fmt.Sprintf("bash %s to-blue --resume-cron", script), 3*time.Minute)
}

func (c *Client) DeployGreenCode(ctx context.Context) (string, error) {
	script := c.cfg.GreenCodeSyncScript
	return c.Run(ctx, fmt.Sprintf("bash %s all", script), 30*time.Minute)
}

func (c *Client) DeployBlueCode(ctx context.Context) (string, error) {
	script := c.cfg.BlueCodeSyncScript
	return c.Run(ctx, fmt.Sprintf("bash %s all", script), 30*time.Minute)
}

func (c *Client) TrafficStatus(ctx context.Context) (string, error) {
	script := c.cfg.TrafficSwitchScript
	return c.Run(ctx, fmt.Sprintf("bash %s status", script), 30*time.Second)
}

func (c *Client) SyncGreenToBlue(ctx context.Context) (string, error) {
	script := c.cfg.StandbySyncScript
	if script == "" {
		script = "/opt/osh-green/005-scripts/osh-prod-standby-sync.sh"
	}
	return c.Run(ctx, fmt.Sprintf("bash %s --green-to-blue", script), 45*time.Minute)
}

// WaitGreenAPI polls green nginx /api/ until 200 or 401 (post GHA deploy).
func (c *Client) WaitGreenAPI(ctx context.Context, port string, maxWait time.Duration) (string, error) {
	return c.WaitSlotAPI(ctx, "green", port, maxWait)
}

// WaitSlotAPI polls nginx /api/ for green (28080) or blue (58080).
func (c *Client) WaitSlotAPI(ctx context.Context, slot, port string, maxWait time.Duration) (string, error) {
	if port == "" {
		if slot == "blue" {
			port = "58080"
		} else {
			port = "28080"
		}
	}
	if c.cfg.MockMode {
		return fmt.Sprintf("[MOCK] %s :%s/api/ healthy", slot, port), nil
	}
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		out, err := c.Run(ctx,
			fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' --max-time 10 http://127.0.0.1:%s/api/ || echo 000", port),
			20*time.Second)
		if err == nil && (out == "200" || out == "401") {
			return fmt.Sprintf("%s :%s/api/ => HTTP %s", slot, port, out), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(15 * time.Second):
		}
	}
	return "", fmt.Errorf("%s API on :%s not healthy within %s", slot, port, maxWait)
}
