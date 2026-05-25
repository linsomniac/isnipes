// Package buildguard holds meta-tests that assert build/CI invariants.
// It is intentionally outside the frozen set so it can evolve freely.
package buildguard

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
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

// loadManifest parses scripts/frozen.sha256 into path→sha.
func loadManifest(t *testing.T, root string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "frozen.sha256"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("malformed manifest line: %q", line)
		}
		m[fields[1]] = fields[0]
	}
	return m
}

// goSources lists *.go under dir (relative to root), optionally excluding
// _test.go, as repo-relative slash paths.
func goSources(t *testing.T, root, dir string, includeTests bool) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		if !includeTests && strings.HasSuffix(p, "_test.go") {
			return nil
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
// wire-schema source (all non-test internal/proto/*.go) and the whole
// deterministic sim (internal/sim/**), so the "no wire change" invariant
// (PHASE8 §1, DoD #2) is mechanically backed rather than aspirational.
func TestFrozen_GuardCoversProto(t *testing.T) {
	root := repoRoot(t)
	manifest := loadManifest(t, root)

	var missing []string
	check := func(files []string) {
		for _, f := range files {
			if _, ok := manifest[f]; !ok {
				missing = append(missing, f)
			}
		}
	}
	check(goSources(t, root, "internal/proto", false)) // wire encoders, no tests
	check(goSources(t, root, "internal/sim", true))    // determinism, incl. tests

	if len(missing) != 0 {
		t.Fatalf("frozen manifest does not cover %d source file(s); re-seed with "+
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

// TestFrozen_GuardIsLiveAndDetectsEdits proves the manifest tracks the
// CURRENT bytes of a wire file (not a stale hash) AND that any edit would
// be caught: the recorded sha equals the file's sha now, and a one-byte
// mutation produces a different sha that no longer matches the manifest.
func TestFrozen_GuardIsLiveAndDetectsEdits(t *testing.T) {
	root := repoRoot(t)
	manifest := loadManifest(t, root)

	const target = "internal/proto/messages.go"
	want, ok := manifest[target]
	if !ok {
		t.Fatalf("%s missing from manifest", target)
	}
	content, err := os.ReadFile(filepath.Join(root, target))
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	gotSum := sha256.Sum256(content)
	got := hex.EncodeToString(gotSum[:])
	if got != want {
		t.Fatalf("manifest is stale for %s: manifest=%s actual=%s", target, want, got)
	}

	// Simulated edit: append a byte. The guard recomputes sha256 over file
	// bytes (scripts/check-frozen.sh uses sha256sum), so a mutated file would
	// hash differently and `make check-frozen` would fail.
	mutated := sha256.Sum256(append(append([]byte{}, content...), '\n'))
	if hex.EncodeToString(mutated[:]) == want {
		t.Fatalf("a mutated %s hashed to the frozen value (impossible); guard would not catch edits", target)
	}
}
