package panel

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDeviceCertificateRenewal(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.data = t.TempDir()
	cfg.socket = ""
	m, err := newNodeManager()
	if err != nil {
		t.Fatal(err)
	}
	defer m.db.Close()
	const id = "device"
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: id}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	raw, err := x509.CreateCertificate(rand.Reader, template, m.pki.ca, pub, m.pki.key)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	previous := peer.SerialNumber.String()
	if _, err = m.db.Exec("INSERT INTO nodes(id,data) VALUES (?,?)", id, `{"mode":"agent"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec("INSERT INTO agent_devices VALUES (?,?)", id, previous); err != nil {
		t.Fatal(err)
	}
	renew := func(auth bool, status int) string {
		t.Helper()
		request := httptest.NewRequest("POST", "https://localhost/renew", nil)
		if auth {
			request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{peer, m.pki.ca}}, PeerCertificates: []*x509.Certificate{peer}}
		}
		response := httptest.NewRecorder()
		m.renewDeviceCertificate(response, request)
		if response.Code != status {
			t.Fatalf("renewal HTTP %d: %s", response.Code, response.Body.String())
		}
		var payload map[string]string
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload["certificate"]
	}
	renew(false, 403)
	certificate := renew(true, 200)
	if retry := renew(true, 200); retry != certificate {
		t.Fatal("retry replaced pending certificate")
	}
	block, _ := pem.Decode([]byte(certificate))
	if block == nil {
		t.Fatal("invalid PEM")
	}
	renewed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.PublicKey.(ed25519.PublicKey).Equal(pub) || !renewed.NotAfter.After(peer.NotAfter) {
		t.Fatal("renewal changed key or did not extend expiry")
	}
	if !m.acceptDeviceCertificate(id, previous) {
		t.Fatal("old certificate lost before activation")
	}
	if !m.acceptDeviceCertificate(id, renewed.SerialNumber.String()) {
		t.Fatal("renewal activation failed")
	}
	if m.acceptDeviceCertificate(id, previous) {
		t.Fatal("superseded certificate still accepted")
	}
	renew(true, 403)
}
