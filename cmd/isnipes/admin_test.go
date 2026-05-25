package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jafo/isnipes/internal/lobby"
	"github.com/jafo/isnipes/internal/match"
	wsnet "github.com/jafo/isnipes/internal/net"
	"github.com/jafo/isnipes/internal/observ"
)

// TestMain_AdminListenerSeparation — /metrics and pprof live on the admin
// mux only; the public mux 404s both; pprof is gated by enablePprof.
// (DoD #9)
func TestMain_AdminListenerSeparation(t *testing.T) {
	metrics := observ.NewRegistry()

	// Public mux must NOT serve metrics or pprof.
	reg := match.NewRegistry(match.RegistryConfig{})
	lob := lobby.NewLobby(lobby.Config{Registry: reg})
	pub := wsnet.NewServer(wsnet.ServerConfig{Lobby: lob, MatchRegistry: reg}).Handler()
	for _, p := range []string{"/metrics", "/debug/pprof/"} {
		rec := httptest.NewRecorder()
		pub.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("public mux %s = %d, want 404 (must not be on the public port)", p, rec.Code)
		}
	}

	get := func(h http.Handler, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code
	}

	// Admin, pprof off: /metrics 200, pprof 404.
	off := newAdminMux(metrics, false)
	if c := get(off, "/metrics"); c != http.StatusOK {
		t.Fatalf("admin /metrics (pprof off) = %d want 200", c)
	}
	if c := get(off, "/debug/pprof/"); c != http.StatusNotFound {
		t.Fatalf("admin /debug/pprof/ (pprof off) = %d want 404", c)
	}

	// Admin, pprof on: both 200.
	on := newAdminMux(metrics, true)
	if c := get(on, "/metrics"); c != http.StatusOK {
		t.Fatalf("admin /metrics (pprof on) = %d want 200", c)
	}
	if c := get(on, "/debug/pprof/"); c != http.StatusOK {
		t.Fatalf("admin /debug/pprof/ (pprof on) = %d want 200", c)
	}
}

// writeSelfSigned writes an ephemeral self-signed cert+key for 127.0.0.1
// into dir and returns their paths. Generated per-test so no key material
// is committed.
func writeSelfSigned(t *testing.T, dir string) (certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("createcert: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certOut, _ := os.Create(certPath)
	_ = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	certOut.Close()
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshalkey: %v", err)
	}
	keyOut, _ := os.Create(keyPath)
	_ = pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	keyOut.Close()
	return certPath, keyPath
}

// TestMain_DirectTLS — with --require-tls, serve() answers HTTPS and a plain
// HTTP request to the same port does not succeed; bad cert paths error.
// (DoD #9c)
func TestMain_DirectTLS(t *testing.T) {
	dir := t.TempDir()
	cert, key := writeSelfSigned(t, dir)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = serve(srv, ln, true, cert, key) }()
	defer srv.Close()
	addr := ln.Addr().String()

	// HTTPS succeeds (self-signed → skip verify).
	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	var ok bool
	for i := 0; i < 50; i++ {
		resp, err := client.Get("https://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ok = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok {
		t.Fatal("HTTPS /healthz did not return 200 with --require-tls")
	}

	// Plain HTTP to the TLS port must NOT return 200.
	plain := &http.Client{Timeout: 2 * time.Second}
	if resp, err := plain.Get("http://" + addr + "/healthz"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("plain HTTP returned 200 against a TLS listener; TLS not enforced")
		}
	}

	// Bad cert paths must error.
	ln2, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln2.Close()
	if err := serve(&http.Server{}, ln2, true, "/no/such/cert", "/no/such/key"); err == nil {
		t.Fatal("serve with missing cert/key returned nil error")
	}
}
