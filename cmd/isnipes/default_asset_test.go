//go:build embed

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestE2E_EmbeddedClientServed — an embed build serves the real client at /
// (references app.js, not the Phase 2 placeholder). Requires `make build`
// (or `npm -C web run build` + copy) to have populated cmd/isnipes/dist
// before `go test -tags embed`. (DoD #11, #13)
func TestE2E_EmbeddedClientServed(t *testing.T) {
	h := http.FileServer(http.FS(embeddedStatic()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d (run `make build` first to populate dist/)", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Phase 2 placeholder") {
		t.Fatal("embed build served the Phase 2 placeholder, not the real client")
	}
	if !strings.Contains(body, "app.js") {
		t.Fatalf("embed build did not serve the real bundle (no app.js reference):\n%s", body)
	}
}
