package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/models"
	"github.com/juege/osh-prod-release/internal/store"
)

const sessionHours = 72

type Service struct {
	store  *store.Store
	cfg    *config.Config
	pepper string
}

func New(cfg *config.Config, st *store.Store) *Service {
	pepper := cfg.AuthPepper
	if pepper == "" {
		pepper = "osh-prod-release"
	}
	return &Service{store: st, cfg: cfg, pepper: pepper}
}

func HashPassword(pepper, password string) string {
	sum := sha256.Sum256([]byte(pepper + ":" + password))
	return hex.EncodeToString(sum[:])
}

func (s *Service) SeedUsers(ctx context.Context) error {
	admin := strings.TrimSpace(s.cfg.SuperAdminUser)
	if admin == "" {
		admin = "juege"
	}
	pass := strings.TrimSpace(s.cfg.SuperAdminPassword)
	if pass == "" {
		pass = "juege123"
	}
	if err := s.store.EnsureUser(ctx, admin, HashPassword(s.pepper, pass), models.RoleAdmin, admin); err != nil {
		return err
	}
	for _, u := range s.cfg.AuthUsers {
		if u.Username == "" || u.Password == "" {
			continue
		}
		role := models.RoleNormal
		if strings.EqualFold(u.Username, admin) {
			role = models.RoleAdmin
		}
		if err := s.store.EnsureUser(ctx, u.Username, HashPassword(s.pepper, u.Password), role, u.DisplayName); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Login(ctx context.Context, username, password string) (string, *models.User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return "", nil, fmt.Errorf("用户名和密码不能为空")
	}
	u, err := s.store.GetUserByUsername(ctx, username)
	if err != nil {
		return "", nil, fmt.Errorf("用户名或密码错误")
	}
	if u.PasswordHash != HashPassword(s.pepper, password) {
		return "", nil, fmt.Errorf("用户名或密码错误")
	}
	token, err := newToken()
	if err != nil {
		return "", nil, err
	}
	expires := time.Now().UTC().Add(sessionHours * time.Hour)
	if err := s.store.CreateSession(ctx, token, u.Username, expires); err != nil {
		return "", nil, err
	}
	pub := u.Public()
	return token, &pub, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, token)
}

func (s *Service) UserFromToken(ctx context.Context, token string) (*models.User, error) {
	if token == "" {
		return nil, fmt.Errorf("missing token")
	}
	sess, err := s.store.GetSession(ctx, token)
	if err != nil {
		return nil, err
	}
	u, err := s.store.GetUserByUsername(ctx, sess.Username)
	if err != nil {
		return nil, err
	}
	pub := u.Public()
	return &pub, nil
}

func (s *Service) IsAdmin(u *models.User) bool {
	if u == nil {
		return false
	}
	return u.Role == models.RoleAdmin
}

func (s *Service) IsBoss(u *models.User) bool {
	if u == nil {
		return false
	}
	if u.Role == models.RoleAdmin {
		return true
	}
	boss := strings.TrimSpace(s.cfg.BossReviewer)
	if boss == "" {
		boss = "juege"
	}
	return strings.EqualFold(u.Username, boss)
}

func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
