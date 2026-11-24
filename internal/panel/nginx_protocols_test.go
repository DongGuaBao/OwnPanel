package panel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func protocolFlag(value bool) *bool { return &value }
func TestSiteIndependentProtocolListeners(t *testing.T) {
	for _, test := range []struct {
		name         string
		s            site
		want, absent []string
	}{
		{"legacy HTTP", site{Port: 9088}, []string{"listen 9088;"}, []string{" ssl;", " quic;"}},
		{"legacy TLS custom port", site{Port: 25573, TLS: true}, []string{"listen 25573 ssl;"}, []string{"listen 443", "listen 80;", "listen 25573;"}},
		{"TLS only", site{Port: 9088, HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(true), TLS: true, TLSPort: 25573}, []string{"listen 25573 ssl;"}, []string{"listen 9088;", "listen 80;", "listen 443"}},
		{"QUIC only", site{HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(false), TLS: true, QUIC: true, QUICPort: 25575}, []string{"listen 25575 quic;", "h3=\":25575\""}, []string{" ssl;", "listen 80;", "listen 443"}},
		{"all custom", site{Port: 25572, HTTPEnabled: protocolFlag(true), HTTPSEnabled: protocolFlag(true), TLS: true, TLSPort: 25573, QUIC: true, QUICPort: 25575}, []string{"listen 25572;", "listen 25573 ssl;", "listen 25575 quic;"}, []string{"listen 443", "listen 80;"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.s.ID = "test"
			test.s.Domains = "example.test"
			test.s.Kind = "locations"
			test.s.Routing = "return 200 ok;"
			if err := validateSiteProtocols(test.s); err != nil {
				t.Fatal(err)
			}
			files := renderSiteAt(test.s, "/fixture")
			text := string(files["server.conf"])
			for _, part := range test.want {
				if !strings.Contains(text, part) {
					t.Fatal(part, text)
				}
			}
			for _, part := range test.absent {
				if strings.Contains(text, part) {
					t.Fatal("unexpected listener", part, text)
				}
			}
			var saved site
			if err := json.Unmarshal(files["site.json"], &saved); err != nil {
				t.Fatal(err)
			}
			h, tls, p := siteProtocols(saved)
			wh, wt, wp := siteProtocols(test.s)
			if h != wh || tls != wt || p != wp || saved.QUICPort != test.s.QUICPort {
				t.Fatal("protocol settings did not persist")
			}
		})
	}
}
func TestSiteProtocolValidation(t *testing.T) {
	for _, s := range []site{
		{HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(false)},
		{HTTPEnabled: protocolFlag(true), Port: 0},
		{HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(true), TLS: true, TLSPort: 0},
		{HTTPEnabled: protocolFlag(true), Port: 12345, HTTPSEnabled: protocolFlag(true), TLS: true, TLSPort: 12345},
		{HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(false), TLS: true, QUIC: true, QUICPort: 65536},
		{HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(false), QUIC: true, QUICPort: 12345},
	} {
		if validateSiteProtocols(s) == nil {
			t.Fatal("accepted invalid listener configuration", s)
		}
	}
	if err := validateSiteProtocols(site{HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(true), TLS: true, TLSPort: 12345, QUIC: true, QUICPort: 12345}); err != nil {
		t.Fatal("TCP and UDP may share a port", err)
	}
}
func TestDeployedCertificateMetadataVisibleWithoutPrivateKey(t *testing.T) {
	chain, key := testCertificatePair(t, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	s := site{TLS: true, CertificateInfo: &certificateInfo{ID: "library-id", Name: "站点证书"}}
	refreshSiteCertificateInfo(&s, func(name string) []byte {
		if name == "certificate.pem" {
			return []byte(chain)
		}
		return nil
	})
	if s.CertificateInfo == nil || s.CertificateInfo.Issuer == "" || s.CertificateInfo.Name != "站点证书" || s.CertificateInfo.ID != "library-id" {
		t.Fatal(s.CertificateInfo)
	}
	raw, _ := json.Marshal(s)
	if strings.Contains(string(raw), "PRIVATE KEY") || strings.Contains(string(raw), key) {
		t.Fatal("metadata leaked private key")
	}
	s.Bindings = []certificateBinding{{ID: "default"}}
	refreshSiteCertificateInfo(&s, func(name string) []byte {
		if name == "certificate.pem" {
			return []byte(chain)
		}
		return nil
	})
	if s.Bindings[0].CertificateInfo == nil {
		t.Fatal("legacy default certificate metadata missing")
	}
}
