package panel

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const deviceRenewalWindow = 30 * 24 * time.Hour

func (m *nodeManager) acceptDeviceCertificate(id, presented string) bool {
	result, err := m.db.Exec(`UPDATE agent_devices SET serial=? WHERE node_id=? AND (serial=? OR EXISTS (
 SELECT 1 FROM agent_renewals WHERE node_id=? AND old_serial=agent_devices.serial AND new_serial=?))`, presented, id, presented, id, presented)
	if err != nil {
		return false
	}
	count, err := result.RowsAffected()
	return err == nil && count == 1
}

func (m *nodeManager) renewDeviceCertificate(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		fail(w, 403, fmt.Errorf("设备证书认证失败"))
		return
	}
	peer := r.TLS.PeerCertificates[0]
	id, previous := peer.Subject.CommonName, peer.SerialNumber.String()
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer tx.Rollback()
	var active string
	if err = tx.QueryRowContext(r.Context(), "SELECT serial FROM agent_devices WHERE node_id=? AND EXISTS (SELECT 1 FROM nodes WHERE id=?)", id, id).Scan(&active); err != nil || active != previous {
		fail(w, 403, fmt.Errorf("设备证书已撤销"))
		return
	}
	if time.Until(peer.NotAfter) > deviceRenewalWindow {
		fail(w, 409, fmt.Errorf("设备证书尚未进入续签窗口"))
		return
	}
	var certificate string
	_ = tx.QueryRowContext(r.Context(), "SELECT certificate FROM agent_renewals WHERE node_id=? AND old_serial=?", id, previous).Scan(&certificate)
	if certificate == "" {
		now := time.Now()
		template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: id}, NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, err := x509.CreateCertificate(rand.Reader, template, m.pki.ca, peer.PublicKey, m.pki.key)
		if err != nil {
			fail(w, 500, err)
			return
		}
		certificate = string(certPEM(der))
		if _, err = tx.ExecContext(r.Context(), "INSERT OR REPLACE INTO agent_renewals VALUES (?,?,?,?)", id, previous, template.SerialNumber.String(), certificate); err != nil {
			fail(w, 500, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, map[string]string{"certificate": certificate})
}

func renewAgentIdentity(center *url.URL, ca *x509.CertPool, current tls.Certificate) tls.Certificate {
	leaf, err := x509.ParseCertificate(current.Certificate[0])
	if err != nil || time.Until(leaf.NotAfter) > deviceRenewalWindow {
		return current
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: ca, Certificates: []tls.Certificate{current}}}
	defer transport.CloseIdleConnections()
	client := http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Post(idURL(center, "/renew"), "application/json", nil)
	if err != nil {
		log.Printf("agent certificate renewal: %v", err)
		return current
	}
	defer response.Body.Close()
	var payload struct {
		Certificate string `json:"certificate"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload) != nil {
		log.Printf("agent certificate renewal returned HTTP %d", response.StatusCode)
		return current
	}
	key, err := os.ReadFile(filepath.Join(cfg.data, "device.key"))
	if err != nil {
		return current
	}
	replacement, err := tls.X509KeyPair([]byte(payload.Certificate), key)
	if err != nil {
		return current
	}
	renewed, err := x509.ParseCertificate(replacement.Certificate[0])
	if err != nil || renewed.Subject.CommonName != leaf.Subject.CommonName || !renewed.NotAfter.After(leaf.NotAfter) {
		return current
	}
	if _, err = renewed.Verify(x509.VerifyOptions{Roots: ca, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return current
	}
	if err = replacePasswordHash(filepath.Join(cfg.data, "device.pem"), []byte(payload.Certificate)); err != nil {
		log.Printf("save renewed agent certificate: %v", err)
		return current
	}
	return replacement
}
