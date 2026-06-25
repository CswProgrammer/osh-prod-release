package ssh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/juege/osh-prod-release/internal/config"
)

func TestUseLocalExecExplicitModes(t *testing.T) {
	cfg := &config.Config{MockMode: false, ProdExecMode: "local"}
	if !useLocalExec(cfg) {
		t.Fatal("expected local mode")
	}
	cfg.ProdExecMode = "ssh"
	if useLocalExec(cfg) {
		t.Fatal("expected ssh mode")
	}
}

func TestUseLocalExecAutoLocalhost(t *testing.T) {
	cfg := &config.Config{
		MockMode:     false,
		ProdExecMode: "auto",
		ProdSSHHost:  "127.0.0.1",
	}
	if !useLocalExec(cfg) {
		t.Fatal("expected auto local for 127.0.0.1")
	}
}

func TestUseLocalExecAutoScriptPresent(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "traffic-switch.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		MockMode:            false,
		ProdExecMode:        "auto",
		ProdSSHHost:         "203.0.113.9",
		TrafficSwitchScript: script,
	}
	if !useLocalExec(cfg) {
		t.Fatal("expected auto local when traffic script exists locally")
	}
}

func TestUseLocalExecAutoRemoteDev(t *testing.T) {
	cfg := &config.Config{
		MockMode:            false,
		ProdExecMode:        "auto",
		ProdSSHHost:         "149.88.92.159",
		TrafficSwitchScript: "/opt/osh-green/005-scripts/osh-traffic-switch.sh",
	}
	if useLocalExec(cfg) {
		t.Fatal("expected ssh on dev machine without local script")
	}
}
