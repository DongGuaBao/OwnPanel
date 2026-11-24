package panel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The mapping file is managed on the agent host by its administrator.
func watchCertificateLineages(mapping string) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			data, err := os.ReadFile(mapping)
			var lineages map[string]string
			if err == nil {
				err = json.Unmarshal(data, &lineages)
			}
			if err != nil {
				log.Printf("certificate mapping: %v", err)
			}
			for id, dir := range lineages {
				if err := syncCertificateLineage(id, dir); err != nil {
					log.Printf("certificate sync for %q: %v", id, err)
				}
			}
			<-ticker.C
		}
	}()
}

func syncCertificateLineage(id, lineage string) error {
	if !validName(id) || !filepath.IsAbs(lineage) {
		return fmt.Errorf("invalid site ID or lineage path")
	}
	cert, err := os.ReadFile(filepath.Join(lineage, "fullchain.pem"))
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(lineage, "privkey.pem"))
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return fmt.Errorf("certificate is outside its validity period")
	}
	nginxMu.Lock()
	defer nginxMu.Unlock()
	dir := filepath.Join(cfg.nginxPrefix, "sites", id)
	raw, err := os.ReadFile(filepath.Join(dir, "site.json"))
	if err != nil {
		return err
	}
	var current site
	if err = json.Unmarshal(raw, &current); err != nil {
		return err
	}
	if !current.TLS {
		return fmt.Errorf("site TLS must be enabled first")
	}
	for _, domain := range strings.Fields(current.Domains) {
		if strings.HasPrefix(domain, "*.") {
			matched := false
			for _, name := range leaf.DNSNames {
				if strings.EqualFold(name, domain) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("certificate does not cover %s", domain)
			}
		} else if err := leaf.VerifyHostname(domain); err != nil {
			return err
		}
	}
	certPath, keyPath := filepath.Join(dir, "certificate.pem"), filepath.Join(dir, "private.key")
	oldCert, err := os.ReadFile(certPath)
	if err != nil {
		return err
	}
	oldKey, err := os.ReadFile(keyPath)
	if err != nil {
		return err
	}
	if bytes.Equal(oldCert, cert) && bytes.Equal(oldKey, key) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = replacePasswordHash(certPath, cert); err != nil {
		return err
	}
	if err = replacePasswordHash(keyPath, key); err == nil {
		_, err = ng(ctx, "-t")
		if err == nil && nginxRunning() {
			_, err = ng(ctx, "-s", "reload")
		}
	}
	if err != nil {
		certErr := replacePasswordHash(certPath, oldCert)
		keyErr := replacePasswordHash(keyPath, oldKey)
		if certErr != nil || keyErr != nil {
			return fmt.Errorf("certificate deployment failed: %v, restore: %v %v", err, certErr, keyErr)
		}
		return err
	}
	audit("nginx_certificate_synced", id)
	return nil
}
