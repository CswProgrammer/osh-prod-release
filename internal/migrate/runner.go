package migrate

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/ssh"
)

type Migration struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Filename    string `json:"filename"`
}

type ExecuteResult struct {
	ID      string `json:"id"`
	Target  string `json:"target"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
}

type Runner struct {
	cfg *config.Config
	ssh *ssh.Client
}

func NewRunner(cfg *config.Config, sshClient *ssh.Client) *Runner {
	return &Runner{cfg: cfg, ssh: sshClient}
}

func (r *Runner) List(ctx context.Context) ([]Migration, error) {
	dir := r.cfg.MigrationsDir
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".sql")
		desc := readFirstComment(filepath.Join(dir, e.Name()))
		out = append(out, Migration{
			ID:          id,
			Name:        e.Name(),
			Description: desc,
			Filename:    e.Name(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func readFirstComment(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--") {
			return strings.TrimSpace(strings.TrimPrefix(line, "--"))
		}
		if line != "" && !strings.HasPrefix(line, "--") {
			break
		}
	}
	return ""
}

func (r *Runner) ReadSQL(id string) (string, error) {
	path := filepath.Join(r.cfg.MigrationsDir, id+".sql")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("migration not found: %s", id)
	}
	return string(raw), nil
}

func (r *Runner) Execute(ctx context.Context, id, actor string) (*ExecuteResult, error) {
	raw, err := r.ReadSQL(id)
	if err != nil {
		return nil, err
	}
	return r.ExecuteRaw(ctx, id, raw, actor)
}

func (r *Runner) ExecuteRaw(ctx context.Context, label, sqlText, actor string) (*ExecuteResult, error) {
	return r.executeRawOn(ctx, "green", label, sqlText, actor)
}

func (r *Runner) ExecuteRawBlue(ctx context.Context, label, sqlText, actor string) (*ExecuteResult, error) {
	return r.executeRawOn(ctx, "blue", label, sqlText, actor)
}

func (r *Runner) executeRawOn(ctx context.Context, slot, label, sqlText, actor string) (*ExecuteResult, error) {
	container, database, password, err := r.mysqlTarget(slot)
	if err != nil {
		return nil, err
	}
	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		return nil, fmt.Errorf("SQL 不能为空")
	}
	if err := guardSQL(sqlText); err != nil {
		return nil, err
	}
	if label == "" {
		label = "custom"
	}

	target := fmt.Sprintf("%s/%s", container, database)
	if r.cfg.MockMode {
		return &ExecuteResult{
			ID: label, Target: target, Success: true,
			Output: fmt.Sprintf("[MOCK] would execute SQL (%d bytes) as %s on %s MySQL", len(sqlText), actor, slot),
		}, nil
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(sqlText))
	safeLabel := strings.NewReplacer("/", "_", " ", "_").Replace(label)
	remote := fmt.Sprintf(
		`set -e
tmp="/tmp/osh-migration-%s.sql"
echo '%s' | base64 -d > "$tmp"
docker exec -i %s mysql -uroot -p'%s' %s < "$tmp"
rm -f "$tmp"
echo "__OSH_SQL_OK__"`,
		safeLabel, b64, container, shellQuote(password), database,
	)

	out, err := r.ssh.Run(ctx, remote, 3*time.Minute)
	res := &ExecuteResult{ID: label, Target: target, Output: out}
	if err != nil {
		res.Success = false
		if out != "" {
			return res, fmt.Errorf("%s", out)
		}
		return res, err
	}
	res.Success = strings.Contains(out, "__OSH_SQL_OK__")
	if !res.Success {
		return res, fmt.Errorf("SQL execution did not complete")
	}
	return res, nil
}

func (r *Runner) mysqlTarget(slot string) (container, database, password string, err error) {
	switch slot {
	case "green":
		if err := r.guardGreenOnly(); err != nil {
			return "", "", "", err
		}
		return r.cfg.GreenMySQLContainer, r.cfg.GreenMySQLDatabase, r.cfg.GreenMySQLRootPassword, nil
	case "blue":
		if err := r.guardBlueOnly(); err != nil {
			return "", "", "", err
		}
		return r.cfg.BlueMySQLContainer, r.cfg.BlueMySQLDatabase, r.cfg.BlueMySQLRootPassword, nil
	default:
		return "", "", "", fmt.Errorf("unknown mysql slot: %s", slot)
	}
}

func (r *Runner) guardBlueOnly() error {
	c := strings.TrimSpace(r.cfg.BlueMySQLContainer)
	if c == "" {
		return fmt.Errorf("BLUE_MYSQL_CONTAINER not configured")
	}
	if c != "osh-mysql" {
		return fmt.Errorf("refusing: blue mysql container must be osh-mysql, got %q", c)
	}
	if strings.Contains(c, "osh-g-") {
		return fmt.Errorf("refusing: green mysql container is not allowed for blue SQL")
	}
	if r.cfg.BlueMySQLRootPassword == "" {
		return fmt.Errorf("BLUE_MYSQL_ROOT_PASSWORD not configured")
	}
	return nil
}

func (r *Runner) guardGreenOnly() error {
	c := strings.TrimSpace(r.cfg.GreenMySQLContainer)
	if c == "" {
		return fmt.Errorf("GREEN_MYSQL_CONTAINER not configured")
	}
	if !strings.Contains(c, "osh-g-") {
		return fmt.Errorf("refusing: container %q is not a green slot (must contain osh-g-)", c)
	}
	if strings.Contains(c, "osh-mysql") && !strings.Contains(c, "osh-g-mysql") {
		return fmt.Errorf("refusing: blue mysql container is not allowed")
	}
	if r.cfg.GreenMySQLRootPassword == "" {
		return fmt.Errorf("GREEN_MYSQL_ROOT_PASSWORD not configured")
	}
	return nil
}

func guardSQL(sql string) error {
	upper := strings.ToUpper(sql)
	blocked := []string{"DROP DATABASE", "TRUNCATE DATABASE", " INTO OUTFILE", " LOAD_FILE("}
	for _, b := range blocked {
		if strings.Contains(upper, b) {
			return fmt.Errorf("blocked SQL pattern: %s", strings.TrimSpace(b))
		}
	}
	return nil
}

func shellQuote(s string) string {
	return strings.ReplaceAll(s, "'", "'\"'\"'")
}
