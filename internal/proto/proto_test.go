package proto_test

import (
	"go/build"
	"strings"
	"testing"
)

// TestProtoImportGraph enforces PHASE2.md §4: no non-test file under
// internal/proto/ imports net, net/http, or any third-party I/O
// package. Test files are exempt (they may import "testing" and
// stdlib helpers like "os" / "path/filepath" for fixture loading).
func TestProtoImportGraph(t *testing.T) {
	pkg, err := build.Default.Import("github.com/jafo/isnipes/internal/proto", "", 0)
	if err != nil {
		t.Fatalf("build.Import: %v", err)
	}
	forbidden := []string{
		"net",
		"net/http",
		"nhooyr.io/websocket",
		"github.com/jafo/isnipes/internal/sim",
		"github.com/jafo/isnipes/internal/net",
		"github.com/jafo/isnipes/internal/match",
		"github.com/jafo/isnipes/internal/lobby",
	}
	for _, imp := range pkg.Imports {
		for _, f := range forbidden {
			if imp == f || strings.HasPrefix(imp, f+"/") {
				t.Errorf("forbidden import in non-test files: %q", imp)
			}
		}
	}
}
