package panel

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestACMEStatusRecovery(t *testing.T) {
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	cfg.data = t.TempDir()
	if err := saveACMEStatus("pending", siteACMEStatus{State: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := saveACMEStatus("complete", siteACMEStatus{State: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	recoverACMEStatuses()
	mux := http.NewServeMux()
	registerSiteACME(mux)
	for id, expected := range map[string]string{"pending": "interrupted", "complete": "succeeded", "new": "idle"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/nginx/sites/"+id+"/acme", nil))
		var result siteACMEStatus
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != 200 || result.State != expected {
			t.Fatalf("%s: want %s, got %d %s (%v)", id, expected, response.Code, response.Body.String(), err)
		}
	}
}

func TestIssueSiteCertificate(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl required")
	}
	if _, err := exec.LookPath("nginx"); err != nil || os.Geteuid() != 0 {
		t.Skip("real nginx configuration check requires nginx and root")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl required for HTTPS verification")
	}
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	cfg.data = t.TempDir()
	prefix, err := os.MkdirTemp("", "ownpanel-acme-nginx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(prefix) })
	if err := os.Chmod(prefix, 0755); err != nil {
		t.Fatal(err)
	}
	cfg.nginxPrefix = prefix
	if err := initNginx(); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ACME_TLS_OK")) }))
	defer upstream.Close()
	s := site{ID: "test", Domains: "example.test", Port: 80, Kind: "proxy", Upstream: upstream.URL}
	dir := filepath.Join(cfg.nginxPrefix, "sites", s.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range renderSite(s) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	lineage := siteLineage(s.ID)
	if err := os.MkdirAll(lineage, 0700); err != nil {
		t.Fatal(err)
	}
	args := []string{"req", "-x509", "-newkey", "ed25519", "-nodes", "-days", "1", "-subj", "/CN=example.test", "-addext", "subjectAltName=DNS:example.test", "-keyout", filepath.Join(lineage, "privkey.pem"), "-out", filepath.Join(lineage, "fullchain.pem")}
	if out, err := exec.Command("openssl", args...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(bin, "certbot"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := issueSiteCertificate(s.ID, s, []string{s.Domains}, "test@example.test"); err != nil {
		t.Fatal(err)
	}
	got, err := readACMESite(s.ID)
	if err != nil || !got.ACME || !got.TLS || got.Port != 443 {
		t.Fatalf("site not migrated: %+v %v", got, err)
	}
	config, err := os.ReadFile(filepath.Join(dir, "server.conf"))
	if err != nil || !strings.Contains(string(config), "listen 80;") || !strings.Contains(string(config), "listen 443 ssl;") {
		t.Fatal("HTTPS or renewal challenge listener missing")
	}
	if _, err := acmeDomains("*.example.test"); err == nil {
		t.Fatal("wildcard accepted for HTTP-01")
	}
	verifyIssuedSiteOverHTTP(t, dir, lineage)
}

func verifyIssuedSiteOverHTTP(t *testing.T, dir, lineage string) {
	t.Helper()
	httpListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer httpListener.Close()
	tlsListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tlsListener.Close()
	httpPort := strconv.Itoa(httpListener.Addr().(*net.TCPAddr).Port)
	tlsPort := strconv.Itoa(tlsListener.Addr().(*net.TCPAddr).Port)
	configPath := filepath.Join(dir, "server.conf")
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// Use loopback high ports so the test can run beside the development sites.
	rewritten := strings.NewReplacer("listen 80;", "listen 127.0.0.1:"+httpPort+";", "listen 443 ssl;", "listen 127.0.0.1:"+tlsPort+" ssl;").Replace(string(config))
	if err := os.WriteFile(configPath, []byte(rewritten), 0644); err != nil {
		t.Fatal(err)
	}
	challenge := filepath.Join(cfg.nginxPrefix, "challenges", ".well-known", "acme-challenge")
	if err := os.MkdirAll(challenge, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(challenge, "sample"), []byte("ACME_HTTP_OK"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = httpListener.Close()
	_ = tlsListener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := ng(ctx); err != nil {
		t.Fatalf("start nginx: %v %s", err, out)
	}
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_, _ = ng(stop, "-s", "quit")
	}()
	curl := func(port string, args ...string) string {
		t.Helper()
		flags := []string{"--silent", "--show-error", "--fail", "--noproxy", "*", "--max-time", "4", "--resolve", "example.test:" + port + ":127.0.0.1"}
		out, err := exec.Command("curl", append(flags, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("curl: %v %s", err, out)
		}
		return string(out)
	}
	if body := curl(httpPort, "http://example.test:"+httpPort+"/.well-known/acme-challenge/sample"); body != "ACME_HTTP_OK" {
		t.Fatalf("challenge response: %q", body)
	}
	if headers := curl(httpPort, "-I", "http://example.test:"+httpPort+"/demo"); !strings.Contains(headers, "301 Moved Permanently") || !strings.Contains(headers, "https://example.test/demo") {
		t.Fatalf("unexpected redirect: %s", headers)
	}
	if body := curl(tlsPort, "--cacert", filepath.Join(lineage, "fullchain.pem"), "https://example.test:"+tlsPort+"/"); body != "ACME_TLS_OK" {
		t.Fatalf("HTTPS response: %q", body)
	}
}
