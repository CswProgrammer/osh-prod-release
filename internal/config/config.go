package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr            string
	DataDir               string
	SQLitePath            string
	MockMode              bool
	ProdSSHHost           string
	ProdSSHPort           int
	ProdSSHUser           string
	ProdSSHPassword       string
	TrafficSwitchScript   string
	GreenCodeSyncScript   string
	BlueCodeSyncScript    string
	GitHubRepo            string
	GitHubBackendRepo          string
	GitHubFrontendRepo         string
	GitHubDispatchRef          string // fallback workflow ref for both repos
	GitHubBackendDispatchRef   string // workflow_dispatch ref (backend)
	GitHubFrontendDispatchRef  string // workflow_dispatch ref (frontend)
	GitHubBackendGitRef        string // git_ref input: branch/SHA to build (backend)
	GitHubFrontendGitRef       string // git_ref input: branch/SHA to build (frontend)
	GitHubToken                string
	AnalyzerURL           string
	BossReviewer          string
	APIToken              string
	GreenMySQLContainer   string
	GreenMySQLDatabase    string
	GreenMySQLRootPassword string
	MigrationsDir         string
}

func Load(path string) (*Config, error) {
	c := &Config{
		ListenAddr:          ":8765",
		DataDir:             "./data",
		SQLitePath:          "./data/platform.db",
		MockMode:            true,
		ProdSSHHost:         "149.88.92.159",
		ProdSSHPort:         16328,
		ProdSSHUser:         "root",
		TrafficSwitchScript: "/opt/osh-green/005-scripts/osh-traffic-switch.sh",
		GreenCodeSyncScript: "/opt/osh-deploy-tools/osh-green-code-sync.sh",
		BlueCodeSyncScript:  "/opt/osh-deploy-tools/osh-prod-code-sync.sh",
		AnalyzerURL:         "http://127.0.0.1:8766",
		BossReviewer:        "觉哥",
		GreenMySQLContainer: "osh-g-mysql",
		GreenMySQLDatabase:  "backstage",
		MigrationsDir:       "./migrations",
	}
	if path == "" {
		return c, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	defer f.Close()

	kv := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		kv[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	if v := kv["LISTEN_ADDR"]; v != "" {
		c.ListenAddr = v
	} else if host, port := kv["BIND_HOST"], kv["BIND_PORT"]; port != "" {
		if host == "" {
			host = "127.0.0.1"
		}
		c.ListenAddr = host + ":" + port
	}
	if v := kv["DATA_DIR"]; v != "" {
		c.DataDir = v
	}
	if v := kv["SQLITE_PATH"]; v != "" {
		c.SQLitePath = v
	}
	if v := kv["MOCK_MODE"]; v != "" {
		c.MockMode = strings.EqualFold(v, "true") || v == "1"
	}
	if v := kv["PROD_SSH_HOST"]; v != "" {
		c.ProdSSHHost = v
	} else if v := kv["PROD_HOST"]; v != "" {
		c.ProdSSHHost = v
	}
	if v := kv["PROD_SSH_PORT"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.ProdSSHPort = p
		}
	} else if v := kv["PROD_PORT"]; v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			c.ProdSSHPort = p
		}
	}
	if v := kv["PROD_SSH_USER"]; v != "" {
		c.ProdSSHUser = v
	} else if v := kv["PROD_USER"]; v != "" {
		c.ProdSSHUser = v
	}
	if v := kv["PROD_SSH_PASSWORD"]; v != "" {
		c.ProdSSHPassword = v
	} else if v := kv["PROD_PASSWORD"]; v != "" && v != "CHANGE_ME" {
		c.ProdSSHPassword = v
	}
	if v := kv["TRAFFIC_SWITCH_SCRIPT"]; v != "" {
		c.TrafficSwitchScript = v
	}
	if v := kv["GREEN_CODE_SYNC_SCRIPT"]; v != "" {
		c.GreenCodeSyncScript = v
	} else if v := kv["PROD_CODE_SYNC_SCRIPT"]; v != "" {
		c.GreenCodeSyncScript = v
	}
	if v := kv["BLUE_CODE_SYNC_SCRIPT"]; v != "" {
		c.BlueCodeSyncScript = v
	}
	c.GitHubRepo = kv["GITHUB_REPO"]
	c.GitHubBackendRepo = kv["GITHUB_BACKEND_REPO"]
	c.GitHubFrontendRepo = kv["GITHUB_FRONTEND_REPO"]
	c.GitHubDispatchRef = kv["GITHUB_DISPATCH_REF"]
	c.GitHubBackendDispatchRef = kv["GITHUB_BACKEND_DISPATCH_REF"]
	c.GitHubFrontendDispatchRef = kv["GITHUB_FRONTEND_DISPATCH_REF"]
	c.GitHubBackendGitRef = kv["GITHUB_BACKEND_GIT_REF"]
	c.GitHubFrontendGitRef = kv["GITHUB_FRONTEND_GIT_REF"]
	c.GitHubToken = kv["GITHUB_TOKEN"]
	if v := kv["ANALYZER_URL"]; v != "" {
		c.AnalyzerURL = v
	}
	if v := kv["BOSS_REVIEWER"]; v != "" {
		c.BossReviewer = v
	}
	c.APIToken = kv["API_TOKEN"]
	if v := kv["GREEN_MYSQL_CONTAINER"]; v != "" {
		c.GreenMySQLContainer = v
	}
	if v := kv["GREEN_MYSQL_DATABASE"]; v != "" {
		c.GreenMySQLDatabase = v
	}
	c.GreenMySQLRootPassword = kv["GREEN_MYSQL_ROOT_PASSWORD"]
	if v := kv["MIGRATIONS_DIR"]; v != "" {
		c.MigrationsDir = v
	}
	return c, sc.Err()
}
