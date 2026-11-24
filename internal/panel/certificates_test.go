package panel

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testCertificatePair(t *testing.T, from, to time.Time) (string, string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Certificate Authority", Organization: []string{"OwnPanel Tests"}}, NotBefore: from.Add(-time.Hour), NotAfter: to.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "*.example.test"}, DNSNames: []string{"*.example.test", "example.test"}, NotBefore: from, NotAfter: to, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) + string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
func certificateRequest(m *nodeManager, node, id, operation, method string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	m.certificates(w, httptest.NewRequest(method, "/", bytes.NewReader(raw)), node, id, operation)
	return w
}
func TestCertificateMetadataValidity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		from, to time.Time
		status   string
	}{{now.Add(-time.Hour), now.Add(90 * 24 * time.Hour), "valid"}, {now.Add(-time.Hour), now.Add(10 * 24 * time.Hour), "expiring"}, {now.Add(-90 * 24 * time.Hour), now.Add(-time.Hour), "expired"}, {now.Add(time.Hour), now.Add(90 * 24 * time.Hour), "notYetValid"}} {
		chain, _ := testCertificatePair(t, test.from, test.to)
		info, normalized, err := inspectCertificate(chain, now)
		if err != nil {
			t.Fatal(err)
		}
		if info.Status != test.status || !info.NotAfter.Equal(test.to) || len(info.Domains) != 2 || !strings.Contains(info.Issuer, "Test Certificate Authority") || len(info.Fingerprint) != 64 || normalized != chain {
			t.Fatalf("invalid metadata: %+v %v", info, err)
		}
	}
}
func TestCertificateLibraryLifecycleAndEncryption(t *testing.T) {
	m := categoryManager(t)
	now := time.Now().UTC().Truncate(time.Second)
	chain, key := testCertificatePair(t, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	payload := map[string]string{"name": "主站证书", "certificate": chain, "privateKey": key}
	post := func(node, id, operation, method string, body any, code int) *httptest.ResponseRecorder {
		t.Helper()
		w := certificateRequest(m, node, id, operation, method, body)
		if w.Code != code {
			t.Fatalf("%s %s/%s: %d %s", method, id, operation, w.Code, w.Body.String())
		}
		return w
	}
	preview := post("local", "", "preview", "POST", payload, 200)
	if strings.Contains(preview.Body.String(), "PRIVATE KEY") {
		t.Fatal("preview exposed secret")
	}
	before := post("local", "", "", "GET", nil, 200)
	if before.Body.String() != "{\"certificates\":[]}\n" {
		t.Fatal("preview persisted a record")
	}
	created := post("local", "", "", "POST", payload, 200)
	var info certificateInfo
	if err := json.Unmarshal(created.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.ID == "" || info.Name != "主站证书" {
		t.Fatal(info)
	}
	var blob []byte
	if err := m.db.QueryRow("SELECT secret FROM certificates WHERE node_id=? AND id=?", "local", info.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("PRIVATE KEY")) || bytes.Contains(blob, []byte(key)) {
		t.Fatal("private key stored in plaintext")
	}
	n := m.aead.NonceSize()
	if _, err := m.aead.Open(nil, blob[:n], blob[n:], certificateAAD("other", info.ID)); err == nil {
		t.Fatal("encrypted key accepted for another host")
	}
	listed := post("local", "", "", "GET", nil, 200)
	if strings.Contains(listed.Body.String(), "PRIVATE KEY") || strings.Contains(listed.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatal("inventory contains PEM contents")
	}
	post("other", info.ID, "export", "POST", nil, 404)
	post("local", info.ID, "export", "GET", nil, 405)
	post("local", "", "", "POST", payload, 409)
	// Open the same project state with a new manager/key loader.
	m.db.Close()
	fresh, err := newNodeManager()
	if err != nil {
		t.Fatal(err)
	}
	m.db = fresh.db
	m.aead = fresh.aead
	exported := post("local", info.ID, "export", "POST", nil, 200)
	var pair map[string]string
	if err = json.Unmarshal(exported.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if pair["privateKey"] != key || pair["certificate"] != chain {
		t.Fatal("persisted pair changed")
	}
	otherChain, otherKey := testCertificatePair(t, now.Add(-time.Hour), now.Add(5*24*time.Hour))
	invalid := map[string]string{"name": "无效更新", "certificate": chain, "privateKey": otherKey}
	post("local", info.ID, "", "PUT", invalid, 400)
	current := post("local", info.ID, "", "GET", nil, 200)
	if !strings.Contains(current.Body.String(), "主站证书") {
		t.Fatal("failed update changed record")
	}
	replacement := map[string]string{"name": "续期证书", "certificate": otherChain, "privateKey": otherKey}
	post("local", info.ID, "", "PUT", replacement, 200)
	post("other", info.ID, "", "DELETE", nil, 404)
	post("local", info.ID, "", "DELETE", nil, 200)
	post("local", info.ID, "", "GET", nil, 404)
}
func TestCertificateRejectsInvalidPairsAndMixedPEM(t *testing.T) {
	m := categoryManager(t)
	now := time.Now()
	chain, key := testCertificatePair(t, now.Add(-time.Hour), now.Add(time.Hour))
	_, wrong := testCertificatePair(t, now.Add(-time.Hour), now.Add(time.Hour))
	for _, pair := range [][2]string{{chain, wrong}, {"not a certificate", key}, {chain + key, key}, {"unexpected text\n" + chain, key}, {chain, "not a key"}} {
		w := certificateRequest(m, "local", "", "", "POST", map[string]string{"name": "test", "certificate": pair[0], "privateKey": pair[1]})
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := certificateRequest(m, "local", "", "", "GET", nil)
	if strings.Contains(w.Body.String(), "test") {
		t.Fatal("invalid input persisted")
	}
}
