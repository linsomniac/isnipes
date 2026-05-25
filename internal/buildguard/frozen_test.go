// Package buildguard holds meta-tests that assert build/CI invariants.
// It is intentionally outside the frozen set so it can evolve freely.
package buildguard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot resolves the module root from this package directory
// (internal/buildguard → ../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %q has no go.mod: %v", root, err)
	}
	return root
}

// loadManifest parses scripts/frozen.sha256 into path→sha and also returns
// the ordered list of repo-relative paths it covers.
func loadManifest(t *testing.T, root string) (map[string]string, []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "frozen.sha256"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m := map[string]string{}
	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("malformed manifest line: %q", line)
		}
		m[fields[1]] = fields[0]
		paths = append(paths, fields[1])
	}
	return m, paths
}

// listFiles walks dir (relative to root) returning repo-relative slash
// paths. When onlyGoNonTest is set, restricts to *.go excluding _test.go;
// otherwise returns every regular file.
func listFiles(t *testing.T, root, dir string, onlyGoNonTest bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if onlyGoNonTest {
			if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return out
}

// TestFrozen_GuardCoversProto asserts the frozen manifest covers every
// wire-schema source (all non-test internal/proto/*.go) and the WHOLE
// deterministic sim tree (internal/sim/** — sources, replay + maze
// fixtures), so the "no wire change / no sim drift" invariant (PHASE8 §1,
// DoD #2) is mechanically backed rather than aspirational.
func TestFrozen_GuardCoversProto(t *testing.T) {
	root := repoRoot(t)
	manifest, _ := loadManifest(t, root)

	var missing []string
	check := func(files []string) {
		for _, f := range files {
			if _, ok := manifest[f]; !ok {
				missing = append(missing, f)
			}
		}
	}
	check(listFiles(t, root, "internal/proto", true)) // wire encoders, no tests
	check(listFiles(t, root, "internal/sim", false))  // ALL files incl. testdata

	if len(missing) != 0 {
		t.Fatalf("frozen manifest does not cover %d file(s); re-seed with "+
			"scripts/check-frozen.sh --write:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}

	// The codex-named encoders must specifically be locked.
	for _, must := range []string{
		"internal/proto/messages.go",
		"internal/proto/frame.go",
		"internal/proto/checksum.go",
	} {
		if _, ok := manifest[must]; !ok {
			t.Fatalf("%s is not in the frozen manifest", must)
		}
	}
}

// copyTree copies each manifest path plus the guard script + manifest into
// dst, preserving the repo-relative layout, so the guard can be executed
// against an isolated copy.
func copyTree(t *testing.T, srcRoot, dst string, paths []string) {
	t.Helper()
	all := append([]string{"scripts/check-frozen.sh", "scripts/frozen.sha256"}, paths...)
	for _, rel := range all {
		data, err := os.ReadFile(filepath.Join(srcRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		dstPath := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(dstPath, data, mode); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// TestFrozen_GuardExecRejectsEdit is an integration test: it copies the
// frozen tree + guard script into a temp dir, confirms the clean copy
// passes, mutates internal/proto/messages.go, and requires the guard to
// EXIT NON-ZERO — establishing the actual guard behavior DoD #2 promises
// ("an edit to messages.go fails it"), not just that SHA-256 is injective.
func TestFrozen_GuardExecRejectsEdit(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available; guard is a bash script")
	}
	root := repoRoot(t)
	_, paths := loadManifest(t, root)

	dst := t.TempDir()
	copyTree(t, root, dst, paths)
	script := filepath.Join(dst, "scripts", "check-frozen.sh")

	// Clean copy must pass.
	if out, err := exec.Command(bash, script).CombinedOutput(); err != nil {
		t.Fatalf("guard failed on a clean copy: %v\n%s", err, out)
	}

	// Mutate a frozen wire file; the guard must now fail.
	target := filepath.Join(dst, "internal", "proto", "messages.go")
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read copied messages.go: %v", err)
	}
	if err := os.WriteFile(target, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("mutate messages.go: %v", err)
	}
	out, err := exec.Command(bash, script).CombinedOutput()
	if err == nil {
		t.Fatalf("guard PASSED after editing messages.go; it must fail.\n%s", out)
	}
}
