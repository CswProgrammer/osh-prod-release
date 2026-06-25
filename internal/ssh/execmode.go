package ssh

import (
	"net"
	"os"
	"strings"

	"github.com/juege/osh-prod-release/internal/config"
)

func useLocalExec(cfg *config.Config) bool {
	if cfg == nil || cfg.MockMode {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.ProdExecMode))
	switch mode {
	case "local":
		return true
	case "ssh", "remote":
		return false
	default:
		if hostIsLocal(cfg.ProdSSHHost) {
			return true
		}
		if cfg.TrafficSwitchScript != "" {
			if _, err := os.Stat(cfg.TrafficSwitchScript); err == nil {
				return true
			}
		}
		return false
	}
}

func hostIsLocal(host string) bool {
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if host == "149.88.92.159" {
		// Common prod IP: treat as local only when this machine actually owns it.
		return machineHasIP(host)
	}
	return machineHasIP(host)
}

func machineHasIP(host string) bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if ip.String() == host {
				return true
			}
		}
	}
	return false
}

// ResolvedExecMode reports how Run() will execute commands (mock/local/ssh).
func ResolvedExecMode(cfg *config.Config) string {
	if cfg == nil {
		return "ssh"
	}
	if cfg.MockMode {
		return "mock"
	}
	if useLocalExec(cfg) {
		return "local"
	}
	return "ssh"
}
