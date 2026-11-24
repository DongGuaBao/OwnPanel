package panel

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"
)

type certificateBinding struct {
	CertificateInfo   *certificateInfo `json:"certificateInfo,omitempty"`
	ID                string           `json:"id"`
	Domains           string           `json:"domains"`
	Certificate       string           `json:"certificate,omitempty"`
	PrivateKey        string           `json:"privateKey,omitempty"`
	CertificateExpiry string           `json:"certificateExpiry,omitempty"`
}

func legacyCertificateName(name string) string {
	if name == "certificate-default.pem" {
		return "certificate.pem"
	}
	if name == "private-default.key" {
		return "private.key"
	}
	return name
}

func normalizeSiteBindings(s *site) error {
	if len(s.Bindings) == 0 {
		return nil
	}
	s.ACME = false
	ids, domains := map[string]bool{}, map[string]bool{}
	all := []string{}
	for _, b := range s.Bindings {
		if !validName(b.ID) || ids[b.ID] {
			return fmt.Errorf("证书绑定 ID 无效或重复")
		}
		ids[b.ID] = true
		names := strings.Fields(b.Domains)
		if len(names) == 0 {
			return fmt.Errorf("每个证书绑定至少需要一个域名")
		}
		for _, name := range names {
			if !validName(strings.TrimPrefix(name, "*.")) || strings.HasPrefix(name, "-") {
				return fmt.Errorf("域名无效: %s", name)
			}
			normalized := strings.TrimSuffix(strings.ToLower(name), ".")
			if domains[normalized] {
				return fmt.Errorf("域名不能重复绑定证书: %s", name)
			}
			domains[normalized] = true
			all = append(all, name)
		}
	}
	s.Domains = strings.Join(all, " ")
	return nil
}

func siteCertificateNames(s site) [][3]string {
	if len(s.Bindings) == 0 {
		return [][3]string{{"certificate.pem", "private.key", s.Domains}}
	}
	pairs := [][3]string{}
	for _, b := range s.Bindings {
		pairs = append(pairs, [3]string{"certificate-" + b.ID + ".pem", "private-" + b.ID + ".key", b.Domains})
	}
	return pairs
}

func validateCertificateData(cert, key []byte, domains string) error {
	if len(key) > 0 {
		if _, err := tls.X509KeyPair(cert, key); err != nil {
			return fmt.Errorf("证书与私钥不匹配: %w", err)
		}
	}
	block, _ := pem.Decode(cert)
	if block == nil {
		return fmt.Errorf("证书 PEM 无效")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}
	if time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return fmt.Errorf("证书不在有效期内")
	}
	for _, name := range strings.Fields(domains) {
		if strings.HasPrefix(name, "*.") {
			covered := false
			for _, dns := range leaf.DNSNames {
				if strings.EqualFold(dns, name) {
					covered = true
					break
				}
			}
			if !covered {
				return fmt.Errorf("证书未覆盖域名 %s", name)
			}
		} else if err := leaf.VerifyHostname(name); err != nil {
			return fmt.Errorf("证书未覆盖域名 %s: %w", name, err)
		}
	}
	return nil
}

// Expose public details of the certificate actually deployed on the host, never
// the private key. Metadata remains visible after a form is reopened or renewed.
func refreshSiteCertificateInfo(s *site, read func(string) []byte) {
	update := func(name string, old *certificateInfo) *certificateInfo {
		data := read(name)
		if len(data) == 0 {
			data = read(legacyCertificateName(name))
		}
		info, _, err := inspectCertificate(string(data), time.Now())
		if err != nil {
			return nil
		}
		if old != nil {
			info.ID = old.ID
			info.Name = old.Name
		}
		return &info
	}
	if s.TLS && len(s.Bindings) == 0 {
		s.CertificateInfo = update("certificate.pem", s.CertificateInfo)
	}
	for i := range s.Bindings {
		s.Bindings[i].CertificateInfo = update("certificate-"+s.Bindings[i].ID+".pem", s.Bindings[i].CertificateInfo)
	}
}
