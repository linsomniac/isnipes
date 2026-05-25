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

	// Dockerfile: multi-stage, DIGEST-PINNED builders, embedded, static,
	// scratch final, public port only, dist wiped, admin loopback (DoD #14).
	df := readRepoFile(t, root, "Dockerfile")
	mustContain(t, "Dockerfile", df,
		"AS web", "AS build", "FROM scratch",
		"-tags embed", "CGO_ENABLED=0",
		"EXPOSE 8080", "rm -rf cmd/isnipes/dist", "--admin-addr=127.0.0.1")
	if n := strings.Count(df, "@sha256:"); n < 2 {
		t.Errorf("Dockerfile builders not digest-pinned: found %d @sha256: pins, want ≥2", n)
	}

	// systemd unit: hardened + BOTH listeners loopback + no caps (DoD #15).
	svc := readRepoFile(t, root, "deploy/isnipes.service")
	mustContain(t, "deploy/isnipes.service", svc,
		"NoNewPrivileges=yes", "DynamicUser=yes", "CapabilityBoundingSet=",
		"--addr=127.0.0.1", "--admin-addr=127.0.0.1", "WantedBy=multi-user.target")

	// nginx: WS upgrade proxy + actually DENIES the admin paths (DoD #16).
	ng := readRepoFile(t, root, "deploy/nginx.conf")
	mustContain(t, "deploy/nginx.conf", ng,
		"proxy_set_header Upgrade", "proxy_pass http://127.0.0.1:8080",
		"location = /metrics", "location /debug/pprof")
	if n := strings.Count(ng, "return 404"); n < 2 {
		t.Errorf("deploy/nginx.conf must deny both admin paths with return 404 (found %d)", n)
	}

	// README: build/docker/systemd/nginx/flags/endpoints (DoD #17).
	rd := readRepoFile(t, root, "deploy/README.md")
	mustContain(t, "deploy/README.md", rd,
		"make build", "docker build", "systemctl",
		"--require-tls", "--admin-addr", "/metrics")
}
