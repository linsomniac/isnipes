//go:build !embed

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMain_EmbedOffNotice — a non-embed (plain `go build`) binary serves the
// "build with -tags embed / --web-dist" notice at /, never the game.
// (DoD #12)
func TestMain_EmbedOffNotice(t *testing.T) {
	h := http.FileServer(http.FS(embeddedStatic()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "without embedded client assets") {
		t.Fatalf("non-embed build did not serve the notice:\n%s", body)
	}
	if strings.Contains(body, "app.js") {
		t.Fatal("non-embed build served the game bundle")
	}
}
