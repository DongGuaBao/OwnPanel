package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"
)

var certbotLock sync.Mutex

type siteACMEStatus struct {
	State   string    `json:"state"`
	Updated time.Time `json:"updated"`
	Error   string    `json:"error,omitempty"`
}

func acmeStatusDir() string { return filepath.Join(cfg.data, "site-acme", "status") }

func saveACMEStatus(id string, status siteACMEStatus) error {
	status.Updated = time.Now().UTC()
	if err := os.MkdirAll(acmeStatusDir(), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return replacePasswordHash(filepath.Join(acmeStatusDir(), id+".json"), data)
}

func recoverACMEStatuses() {
	files, err := os.ReadDir(acmeStatusDir())
	if err != nil && !os.IsNotExist(err) {
		log.Printf("read ACME status directory: %v", err)
	}
	for _, file := range files {
		id := strings.TrimSuffix(file.Name(), ".json")
		if !strings.HasSuffix(file.Name(), ".json") || !validName(id) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(acmeStatusDir(), file.Name()))
		var status siteACMEStatus
		if err == nil && json.Unmarshal(data, &status) == nil && status.State == "running" {
			if err = saveACMEStatus(id, siteACMEStatus{State: "interrupted", Error: "Agent 重启时申请中断，请检查站点后重新提交"}); err != nil {
				log.Printf("recover ACME status for %s: %v", id, err)
			}
		}
	}
}

var dnsLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

func acmeDomains(input string) ([]string, error) {
	domains := strings.Fields(input)
	if len(domains) == 0 || len(domains) > 100 {
		return nil, fmt.Errorf("需要 1 至 100 个 DNS 域名")
	}
	for _, domain := range domains {
		labels := strings.Split(domain, ".")
		if len(domain) > 253 || len(labels) < 2 {
			return nil, fmt.Errorf("无效域名: %s", domain)
		}
		for _, label := range labels {
			if !dnsLabel.MatchString(label) {
				return nil, fmt.Errorf("HTTP-01 不支持该域名: %s", domain)
			}
		}
	}
	return domains, nil
}

func readACMESite(id string) (site, error) {
	var s site
	data, err := os.ReadFile(filepath.Join(cfg.nginxPrefix, "sites", id, "site.json"))
	if err == nil {
		err = json.Unmarshal(data, &s)
	}
	return s, err
}

func runSiteCertbot(ctx context.Context, args ...string) error {
	base := filepath.Join(cfg.data, "site-acme")
	flags := []string{"--non-interactive", "--config-dir", filepath.Join(base, "config"), "--work-dir", filepath.Join(base, "work"), "--logs-dir", filepath.Join(base, "logs"), "--no-directory-hooks"}
	_, err := command(ctx, "certbot", append(args, flags...)...)
	return err
}

func siteLineage(id string) string { return filepath.Join(cfg.data, "site-acme", "config", "live", id) }

func registerSiteACME(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nginx/sites/{id}/acme", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !validName(id) {
			fail(w, 400, fmt.Errorf("站点 ID 无效"))
			return
		}
		data, err := os.ReadFile(filepath.Join(acmeStatusDir(), id+".json"))
		if os.IsNotExist(err) {
			respond(w, siteACMEStatus{State: "idle"})
			return
		}
		var status siteACMEStatus
		if err != nil || json.Unmarshal(data, &status) != nil {
			fail(w, 500, fmt.Errorf("无法读取证书任务状态"))
			return
		}
		respond(w, status)
	})
	mux.HandleFunc("POST /api/nginx/sites/{id}/acme", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !validName(id) {
			fail(w, 400, fmt.Errorf("站点 ID 无效"))
			return
		}
		var body struct {
			Email   string `json:"email"`
			TLSPort int    `json:"tlsPort"`
		}
		if err := decode(w, r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		email, err := mail.ParseAddress(body.Email)
		if err != nil || email.Address != body.Email || strings.HasPrefix(body.Email, "-") {
			fail(w, 400, fmt.Errorf("请输入有效邮箱"))
			return
		}
		if _, err = exec.LookPath("certbot"); err != nil {
			fail(w, 409, fmt.Errorf("请先在 Agent 主机安装 Certbot"))
			return
		}
		if !certbotLock.TryLock() {
			fail(w, 409, fmt.Errorf("已有证书任务正在运行"))
			return
		}
		nginxMu.Lock()
		current, err := readACMESite(id)
		running := nginxRunning()
		nginxMu.Unlock()
		domains, domainErr := acmeDomains(current.Domains)
		if err != nil || domainErr != nil || !running || current.Manual || current.TLS || current.Port != 80 {
			certbotLock.Unlock()
			fail(w, 409, fmt.Errorf("签发需要运行中的表单管理 HTTP 站点、80 端口和有效 DNS 域名"))
			return
		}
		if (current.HTTPEnabled != nil || body.TLSPort != 0) && (body.TLSPort < 1 || body.TLSPort > 65535 || body.TLSPort == current.Port) {
			certbotLock.Unlock()
			fail(w, 400, fmt.Errorf("请选择与 HTTP 不同的有效 SSL 端口"))
			return
		}
		if err := saveACMEStatus(id, siteACMEStatus{State: "running"}); err != nil {
			certbotLock.Unlock()
			fail(w, 500, fmt.Errorf("无法保存证书任务状态"))
			return
		}
		go func() {
			defer certbotLock.Unlock()
			status := siteACMEStatus{State: "succeeded"}
			if err := issueSiteCertificate(id, current, domains, email.Address, body.TLSPort); err != nil {
				status = siteACMEStatus{State: "failed", Error: "证书签发或部署失败，请查看 Agent 日志"}
				log.Printf("ACME %s: %v", id, err)
				audit("nginx_acme_failed", map[string]string{"site": id, "error": err.Error()})
			}
			if err := saveACMEStatus(id, status); err != nil {
				log.Printf("save ACME status for %s: %v", id, err)
			}
		}()
		audit("nginx_acme_requested", id)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		respond(w, map[string]bool{"queued": true})
	})
}

func issueSiteCertificate(id string, original site, domains []string, email string, requestedPort ...int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	args := []string{"certonly", "--webroot", "--webroot-path", filepath.Join(cfg.nginxPrefix, "challenges"), "--cert-name", id, "--agree-tos", "--email=" + email, "--keep-until-expiring"}
	for _, domain := range domains {
		args = append(args, "-d", domain)
	}
	if err := runSiteCertbot(ctx, args...); err != nil {
		return err
	}
	cert, err := os.ReadFile(filepath.Join(siteLineage(id), "fullchain.pem"))
	if err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(siteLineage(id), "privkey.pem"))
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
	for _, domain := range domains {
		if err := leaf.VerifyHostname(domain); err != nil {
			return err
		}
	}
	nginxMu.Lock()
	defer nginxMu.Unlock()
	current, err := readACMESite(id)
	if err != nil || !reflect.DeepEqual(current, original) {
		return fmt.Errorf("站点在签发期间发生变更，请重新提交")
	}
	custom, err := os.ReadFile(filepath.Join(cfg.nginxPrefix, "sites", id, "custom.conf"))
	if err != nil {
		return err
	}
	current.Custom = string(custom)
	targetPort := 443 // Compatibility for old callers of the ACME-specific endpoint.
	if len(requestedPort) > 0 && requestedPort[0] > 0 {
		targetPort = requestedPort[0]
	}
	current.TLS, current.ACME = true, true
	if original.HTTPEnabled != nil || targetPort != 443 {
		enabled := true
		current.HTTPEnabled = &enabled
		current.HTTPSEnabled = &enabled
		current.TLSPort = targetPort
	} else {
		current.Port = targetPort
	}
	current.Certificate, current.PrivateKey = string(cert), string(key)
	deployCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if _, err = applySite(deployCtx, current); err != nil {
		return err
	}
	audit("nginx_acme_issued", id)
	return nil
}

func startSiteRenewals() {
	go func() {
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			if _, err := exec.LookPath("certbot"); err == nil && certbotLock.TryLock() {
				entries, _ := os.ReadDir(filepath.Join(cfg.nginxPrefix, "sites"))
				for _, entry := range entries {
					id := entry.Name()
					if !entry.IsDir() || !validName(id) {
						continue
					}
					s, err := readACMESite(id)
					if err != nil || !s.ACME || !s.TLS {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					err = runSiteCertbot(ctx, "renew", "--cert-name", id, "--no-random-sleep-on-renew")
					cancel()
					if err == nil {
						err = syncCertificateLineage(id, siteLineage(id))
					}
					if err != nil {
						log.Printf("renew site certificate %s: %v", id, err)
					}
				}
				certbotLock.Unlock()
			}
			<-ticker.C
		}
	}()
}
