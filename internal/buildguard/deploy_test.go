package buildguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func mustContain(t *testing.T, rel, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("%s missing expected content %q", rel, s)
		}
	}
}

// TestDeploy_FilesPresent — the deploy artifacts exist and have the
// load-bearing shape (PHASE8 §11-§13, DoD #14-#17, §16.5). Executing the
// Docker build / running nginx is deferred-to-operator; this asserts the
// files are present and correctly wired.
func TestDeploy_FilesPresent(t *testing.T) {
	root := repoRoot(t)

	for _, rel := range []string{
		"Dockerfile", ".dockerignore",
		"deploy/isnipes.service", "deploy/nginx.conf", "deploy/README.md",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("missing deploy artifact %s: %v", rel, err)
		}
	}

	// Dockerfile: multi-stage, embedded, static, scratch final (DoD #14).
	df := readRepoFile(t, root, "Dockerfile")
	mustContain(t, "Dockerfile", df,
		"AS web", "AS build", "FROM scratch",
		"-tags embed", "CGO_ENABLED=0")

	// systemd unit: hardened + loopback listeners (DoD #15).
	svc := readRepoFile(t, root, "deploy/isnipes.service")
	mustContain(t, "deploy/isnipes.service", svc,
		"NoNewPrivileges=yes", "DynamicUser=yes",
		"--admin-addr=127.0.0.1", "WantedBy=multi-user.target")

	// nginx: WS upgrade proxy + denies the admin paths (DoD #16).
	ng := readRepoFile(t, root, "deploy/nginx.conf")
	mustContain(t, "deploy/nginx.conf", ng,
		"proxy_set_header Upgrade", "proxy_pass http://127.0.0.1:8080")
	if !strings.Contains(ng, "/metrics") || !strings.Contains(ng, "/debug/pprof") {
		t.Error("deploy/nginx.conf must reference and deny /metrics and /debug/pprof")
	}

	// README: build/docker/systemd/nginx/flags/endpoints (DoD #17).
	rd := readRepoFile(t, root, "deploy/README.md")
	mustContain(t, "deploy/README.md", rd,
		"make build", "docker build", "systemctl",
		"--require-tls", "--admin-addr", "/metrics")
}
