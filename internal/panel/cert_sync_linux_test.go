package panel

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSyncCertificateLineage(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl required")
	}
	root := t.TempDir()
	previous := cfg
	t.Cleanup(func() { cfg = previous })
	cfg.data = root
	cfg.nginxPrefix = root
	siteDir := filepath.Join(root, "sites", "example")
	lineage := filepath.Join(root, "lineage")
	for _, dir := range []string{siteDir, lineage} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	certPath := filepath.Join(siteDir, "certificate.pem")
	keyPath := filepath.Join(siteDir, "private.key")
	write(certPath, "old certificate")
	write(keyPath, "old key")
	write(filepath.Join(siteDir, "site.json"), `{"id":"example","domains":"example.test","tls":true}`)
	custom := filepath.Join(siteDir, "custom.conf")
	write(custom, "# preserve manual changes")
	cert, key := filepath.Join(lineage, "fullchain.pem"), filepath.Join(lineage, "privkey.pem")
	if out, err := exec.Command("openssl", "req", "-x509", "-newkey", "ed25519", "-nodes", "-days", "1", "-subj", "/CN=example.test", "-addext", "subjectAltName=DNS:example.test", "-keyout", key, "-out", cert).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	nginx := filepath.Join(root, "nginx")
	t.Setenv("PATH", root+":"+os.Getenv("PATH"))
	stub := func(body string) {
		t.Helper()
		write(nginx, "#!/bin/sh\n"+body+"\n")
		if err := os.Chmod(nginx, 0700); err != nil {
			t.Fatal(err)
		}
	}
	stub("exit 1")
	if err := syncCertificateLineage("example", lineage); err == nil {
		t.Fatal("accepted a failed Nginx configuration test")
	}
	if read(certPath) != "old certificate" || read(keyPath) != "old key" {
		t.Fatal("failed to restore certificate pair")
	}
	stub("exit 0")
	if err := syncCertificateLineage("example", lineage); err != nil {
		t.Fatal(err)
	}
	if read(certPath) != read(cert) || read(keyPath) != read(key) || read(custom) != "# preserve manual changes" {
		t.Fatal("certificate sync changed unexpected content")
	}
	st, err := os.Stat(keyPath)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("private key permissions changed")
	}
}
