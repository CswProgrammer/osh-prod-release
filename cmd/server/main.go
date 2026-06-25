package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/juege/osh-prod-release/internal/api"
	"github.com/juege/osh-prod-release/internal/auth"
	"github.com/juege/osh-prod-release/internal/config"
	"github.com/juege/osh-prod-release/internal/release"
	"github.com/juege/osh-prod-release/internal/ssh"
	"github.com/juege/osh-prod-release/internal/store"
	"github.com/juege/osh-prod-release/internal/traffic"
)

//go:embed all:static
var staticFS embed.FS

func main() {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "config.env"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if v := os.Getenv("LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	sshClient := ssh.New(cfg)
	trafficSvc := traffic.New(st, sshClient)
	svc := release.New(cfg, st, trafficSvc)
	authSvc := auth.New(cfg, st)
	if err := authSvc.SeedUsers(context.Background()); err != nil {
		log.Fatal(err)
	}
	handler := api.New(cfg, svc, authSvc)

	mux := http.NewServeMux()
	handler.Register(mux)

	// static web (built to static/ or fallback index)
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", spaHandler(sub))

	log.Printf("OSH Deploy Platform listening on %s (mock=%v)", cfg.ListenAddr, cfg.MockMode)
	if err := http.ListenAndServe(cfg.ListenAddr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Avoid stale embedded UI after go run restarts (browser caches /app.js aggressively).
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		if r.URL.Path != "/" && !fileExists(fsys, r.URL.Path) {
			r2 := *r
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, &r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func fileExists(fsys fs.FS, path string) bool {
	path = filepath.Clean(path)
	if path == "." || path == "/" {
		return true
	}
	_, err := fs.Stat(fsys, path[1:])
	return err == nil
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
