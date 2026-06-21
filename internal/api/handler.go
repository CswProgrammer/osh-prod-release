package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/migrate"
	"github.com/juege/osh-prod-release/internal/models"
	"github.com/juege/osh-prod-release/internal/release"
	"github.com/juege/osh-prod-release/internal/ssh"
)

type Handler struct {
	cfg     *config.Config
	release *release.Service
	migrate *migrate.Runner
}

func New(cfg *config.Config, svc *release.Service) *Handler {
	return &Handler{
		cfg:     cfg,
		release: svc,
		migrate: migrate.NewRunner(cfg, ssh.New(cfg)),
	}
}

func (h *Handler) auth(w http.ResponseWriter, r *http.Request) bool {
	if h.cfg.APIToken == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if strings.TrimPrefix(auth, "Bearer ") == h.cfg.APIToken {
		return true
	}
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	return false
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/deploy/active", h.deployActive)
	mux.HandleFunc("GET /api/releases", h.listReleases)
	mux.HandleFunc("POST /api/releases", h.createRelease)
	mux.HandleFunc("GET /api/releases/{id}", h.getRelease)
	mux.HandleFunc("POST /api/releases/{id}/submit-review", h.submitReview)
	mux.HandleFunc("POST /api/releases/{id}/boss-approve", h.bossApprove)
	mux.HandleFunc("POST /api/releases/{id}/deploy", h.deploy)
	mux.HandleFunc("POST /api/releases/{id}/switch", h.switchTraffic)
	mux.HandleFunc("POST /api/releases/{id}/verify", h.manualVerify)
	mux.HandleFunc("POST /api/releases/{id}/rollback", h.rollback)
	mux.HandleFunc("GET /api/traffic/status", h.trafficStatus)
	mux.HandleFunc("POST /api/items/{itemId}/reviews", h.submitItemReview)
	mux.HandleFunc("GET /api/migrations", h.listMigrations)
	mux.HandleFunc("GET /api/migrations/{id}", h.getMigrationSQL)
	mux.HandleFunc("POST /api/migrations/{id}/execute", h.executeMigration)
	mux.HandleFunc("POST /api/sql/execute", h.executeSQL)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	ghaEnabled := h.cfg.GitHubToken != "" &&
		(h.cfg.GitHubBackendRepo != "" || h.cfg.GitHubFrontendRepo != "")
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"mock_mode": h.cfg.MockMode,
		"deploy": map[string]any{
			"target":        "green",
			"green_url":     fmt.Sprintf("http://%s:28080/", h.cfg.ProdSSHHost),
			"prod_host":     h.cfg.ProdSSHHost,
			"gha_enabled":   ghaEnabled,
			"backend_repo":  h.cfg.GitHubBackendRepo,
			"frontend_repo": h.cfg.GitHubFrontendRepo,
			"backend_ref":   h.cfg.GitHubBackendGitRef,
			"frontend_ref":  h.cfg.GitHubFrontendGitRef,
			"boss_reviewer": h.cfg.BossReviewer,
		},
		"mysql": map[string]any{
			"green_container": h.cfg.GreenMySQLContainer,
			"database":        h.cfg.GreenMySQLDatabase,
			"configured":      h.cfg.GreenMySQLRootPassword != "",
		},
	})
}

func (h *Handler) listReleases(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	list, err := h.release.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) createRelease(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	var req models.CreateReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	rel, err := h.release.Create(r.Context(), req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rel)
}

func (h *Handler) getRelease(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	rel, err := h.release.Get(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) submitReview(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.ActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Actor == "" {
		req.Actor = "system"
	}
	rel, err := h.release.SubmitForReview(r.Context(), id, req.Actor)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) bossApprove(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.BossApproveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	rel, err := h.release.BossApprove(r.Context(), id, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) deployActive(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	active, err := h.release.GetActiveDeploy(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if active == nil {
		writeJSON(w, http.StatusOK, map[string]any{"busy": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"busy":   true,
		"id":     active.ID,
		"title":  active.Title,
		"status": active.Status,
	})
}

func (h *Handler) deploy(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.ActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Actor == "" {
		req.Actor = "ops"
	}
	rel, err := h.release.StartDeploy(r.Context(), id, req.Actor)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) switchTraffic(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.ActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Actor == "" {
		req.Actor = "ops"
	}
	rel, err := h.release.SwitchTraffic(r.Context(), id, req.Actor)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) manualVerify(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.ActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Actor == "" {
		req.Actor = "owner"
	}
	rel, err := h.release.ConfirmManualVerify(r.Context(), id, req.Actor)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) rollback(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Actor == "" {
		req.Actor = "ops"
	}
	rel, err := h.release.Rollback(r.Context(), id, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) trafficStatus(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	out, err := h.release.TrafficStatus(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": out})
}

func (h *Handler) submitItemReview(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	itemID := r.PathValue("itemId")
	var req models.SubmitReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	rel, err := h.release.SubmitReview(r.Context(), itemID, req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

func (h *Handler) listMigrations(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	list, err := h.migrate.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (h *Handler) executeMigration(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	var req models.ActionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Actor == "" {
		req.Actor = "ops"
	}
	res, err := h.migrate.Execute(r.Context(), id, req.Actor)
	if err != nil {
		if res != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": res})
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) getMigrationSQL(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	id := r.PathValue("id")
	sql, err := h.migrate.ReadSQL(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id, "sql": sql})
}

func (h *Handler) executeSQL(w http.ResponseWriter, r *http.Request) {
	if !h.auth(w, r) {
		return
	}
	var req struct {
		SQL   string `json:"sql"`
		Actor string `json:"actor"`
		Label string `json:"label"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Actor == "" {
		req.Actor = "ops"
	}
	res, err := h.migrate.ExecuteRaw(r.Context(), req.Label, req.SQL, req.Actor)
	if err != nil {
		if res != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "result": res})
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}
