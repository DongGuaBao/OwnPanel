package panel

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestNodeCredentialEncryptionBoundToIdentity(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg.data = t.TempDir()
	cfg.socket = ""
	cfg.agentHosts = "localhost"
	m, e := newNodeManager()
	if e != nil {
		t.Fatal(e)
	}
	defer m.db.Close()
	secret := SSHSecret{PrivateKey: "private-key-test-content", Passphrase: "passphrase-test"}
	blob, e := m.encrypt("node-a", secret)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(blob, []byte(secret.PrivateKey)) {
		t.Fatal("plaintext persisted")
	}
	n := m.aead.NonceSize()
	if _, e = m.aead.Open(nil, blob[:n], blob[n:], []byte("node-b")); e == nil {
		t.Fatal("credential accepted for another node")
	}
	if _, e = m.db.Exec("INSERT INTO nodes(id,data,secret) VALUES(?,?,?)", "node-a", "{}", blob); e != nil {
		t.Fatal(e)
	}
	got, e := m.secret("node-a")
	if e != nil || got != secret {
		t.Fatalf("decrypt: %+v %v", got, e)
	}
}
func TestMissingMasterKeyFailsClosed(t *testing.T) {
	old := cfg
	defer func() { cfg = old }()
	cfg.data = t.TempDir()
	if e := os.WriteFile(filepath.Join(cfg.data, "control.db"), []byte("existing"), 0600); e != nil {
		t.Fatal(e)
	}
	if m, e := newNodeManager(); e == nil {
		m.db.Close()
		t.Fatal("silently replaced missing master key")
	}
}
func TestBoundedSSHOutputConcurrent(t *testing.T) {
	b := &boundedOutput{limit: 10000}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if _, e := b.Write([]byte("x")); e != nil {
					t.Error(e)
				}
			}
		}()
	}
	wg.Wait()
	if len(b.String()) != 1000 {
		t.Fatal("lost concurrent output")
	}
	if _, e := b.Write([]byte(strings.Repeat("x", 10001))); e == nil {
		t.Fatal("output limit ignored")
	}
}
func TestRemotePathValidation(t *testing.T) {
	d := newSSHDriver(Node{FileRoot: "/srv/site"}, SSHSecret{})
	for _, p := range []string{"/../../etc/passwd", "/a/../b", "/a\\b", "/a\n"} {
		if _, e := d.remotePath(p); e == nil {
			t.Fatalf("unsafe path accepted: %q", p)
		}
	}
	p, e := d.remotePath("/index.html")
	if e != nil || p != "/srv/site/index.html" {
		t.Fatalf("mapping: %s %v", p, e)
	}
}
