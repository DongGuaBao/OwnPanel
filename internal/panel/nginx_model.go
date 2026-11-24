package panel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type site struct {
	HTTPEnabled     *bool                `json:"httpEnabled,omitempty"`
	HTTPSEnabled    *bool                `json:"httpsEnabled,omitempty"`
	TLSPort         int                  `json:"tlsPort,omitempty"`
	QUIC            bool                 `json:"quic,omitempty"`
	QUICPort        int                  `json:"quicPort,omitempty"`
	CertificateInfo *certificateInfo     `json:"certificateInfo,omitempty"`
	Bindings        []certificateBinding `json:"bindings,omitempty"`
	Manual          bool                 `json:"manual"`
	ID              string               `json:"id"`
	Domains         string               `json:"domains"`
	Port            int                  `json:"port"`
	Kind            string               `json:"kind"`
	Routing         string               `json:"routing,omitempty"`
	RouteForm       json.RawMessage      `json:"routeForm,omitempty"`
	Root            string               `json:"root"`
	Upstream        string               `json:"upstream"`
	SPA             bool                 `json:"spa"`
	TLS             bool                 `json:"tls"`
	ACME            bool                 `json:"acme,omitempty"`
	Custom          string               `json:"custom"`
	Certificate     string               `json:"certificate,omitempty"`
	PrivateKey      string               `json:"privateKey,omitempty"`
}

func q(s string) string      { return strconv.Quote(s) }
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func renderSiteAt(s site, prefix string) map[string][]byte {
	dir := path.Join(prefix, "sites", s.ID)
	httpEnabled, httpsEnabled, tlsPort := siteProtocols(s)
	listeners := ""
	if httpEnabled {
		listeners += fmt.Sprintf(" listen %d;\n", s.Port)
	}
	if httpsEnabled {
		listeners += fmt.Sprintf(" listen %d ssl;\n", tlsPort)
	}
	if s.QUIC {
		listeners += fmt.Sprintf(" listen %d quic;\n http3 on;\n add_header Alt-Svc 'h3=\":%d\"; ma=86400' always;\n", s.QUICPort, s.QUICPort)
	}
	renderServer := func(domains, cert, key string) string {
		certificateDirectives := ""
		if cert != "" {
			certificateDirectives = fmt.Sprintf(" ssl_certificate %s;\n ssl_certificate_key %s;\n", q(path.Join(dir, cert)), q(path.Join(dir, key)))
		}
		return fmt.Sprintf("# Managed by OwnPanel; edit custom.conf for custom directives.\nserver {\n%s server_name %s;\n access_log %s;\n error_log %s;\n #CERT-APPLY-CHECK--START\n location ^~ /.well-known/acme-challenge/ { root %s; }\n #CERT-APPLY-CHECK--END\n%s include %s;\n include %s;\n include %s;\n}\n", listeners, strings.Join(strings.Fields(domains), " "), q(path.Join(prefix, "logs", s.ID+".access.log")), q(path.Join(prefix, "logs", s.ID+".error.log")), q(path.Join(prefix, "challenges")), certificateDirectives, q(path.Join(dir, "routing.conf")), q(path.Join(dir, "tls.conf")), q(path.Join(dir, "custom.conf")))
	}
	server := renderServer(s.Domains, "", "")
	routing := ""
	if s.Kind == "locations" {
		routing = s.Routing + "\n"
	} else if s.Kind == "proxy" {
		routing = fmt.Sprintf("location / {\n proxy_pass %s;\n proxy_http_version 1.1;\n proxy_set_header Host $host;\n proxy_set_header X-Real-IP $remote_addr;\n proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n proxy_set_header X-Forwarded-Proto $scheme;\n proxy_set_header Upgrade $http_upgrade;\n proxy_set_header Connection $connection_upgrade;\n proxy_read_timeout 300s;\n}\n", s.Upstream)
	} else {
		fallback := "=404"
		if s.SPA {
			fallback = "/index.html"
		}
		routing = fmt.Sprintf("root %s;\nindex index.html;\nlocation / { try_files $uri $uri/ %s; }\n", q(s.Root), fallback)
	}
	tlsConfig := ""
	if s.TLS {
		tlsConfig = fmt.Sprintf("ssl_certificate %s;\nssl_certificate_key %s;\nssl_protocols TLSv1.2 TLSv1.3;\n", q(path.Join(dir, "certificate.pem")), q(path.Join(dir, "private.key")))
	}
	if s.TLS && len(s.Bindings) > 0 {
		server = ""
		tlsConfig = "ssl_protocols TLSv1.2 TLSv1.3;\n"
		for _, binding := range s.Bindings {
			server += renderServer(binding.Domains, "certificate-"+binding.ID+".pem", "private-"+binding.ID+".key")
		}
	}
	cert, key := s.Certificate, s.PrivateKey
	if s.ACME && s.TLS && s.Port == 443 && s.HTTPEnabled == nil {
		server += fmt.Sprintf("server {\n listen 80;\n server_name %s;\n location ^~ /.well-known/acme-challenge/ { root %s; }\n location / { return 301 https://$host$request_uri; }\n}\n", strings.Join(strings.Fields(s.Domains), " "), q(path.Join(prefix, "challenges")))
	}
	bindings := s.Bindings
	s.Bindings = append([]certificateBinding(nil), s.Bindings...)
	for i := range s.Bindings {
		s.Bindings[i].CertificateExpiry = ""
		s.Bindings[i].Certificate = ""
		s.Bindings[i].PrivateKey = ""
	}
	s.Certificate = ""
	s.PrivateKey = ""
	b, _ := json.MarshalIndent(s, "", "  ")
	files := map[string][]byte{"site.json": b, "server.conf": []byte(server), "routing.conf": []byte(routing), "tls.conf": []byte(tlsConfig), "custom.conf": []byte(s.Custom)}
	if cert != "" || key != "" {
		files["certificate.pem"] = []byte(cert)
		files["private.key"] = []byte(key)
	}
	for _, binding := range bindings {
		if binding.Certificate != "" {
			files["certificate-"+binding.ID+".pem"] = []byte(binding.Certificate)
		}
		if binding.PrivateKey != "" {
			files["private-"+binding.ID+".key"] = []byte(binding.PrivateKey)
		}
	}
	return files
}

func validateRemoteSite(s site, root string) error {
	if err := validateSiteProtocols(s); err != nil {
		return err
	}
	if !validName(s.ID) {
		return fmt.Errorf("站点 ID 或端口无效")
	}
	if len(strings.Fields(s.Domains)) == 0 {
		return fmt.Errorf("请输入域名")
	}
	for _, d := range strings.Fields(s.Domains) {
		if !validName(strings.TrimPrefix(d, "*.")) {
			return fmt.Errorf("域名无效")
		}
	}
	switch s.Kind {
	case "locations":
		if strings.ContainsRune(s.Routing, 0) {
			return fmt.Errorf("路由配置包含无效字符")
		}
	case "proxy":
		u, e := url.Parse(s.Upstream)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.ContainsAny(s.Upstream, "\r\n\t ;{}\"\\$") {
			return fmt.Errorf("反代地址无效")
		}
	case "static":
		if !strings.HasPrefix(s.Root, "/") || strings.ContainsAny(s.Root, "\x00\r\n") {
			return fmt.Errorf("静态目录无效")
		}
		p := path.Clean(s.Root)
		base := path.Clean(root)
		if base != "/" && p != base && !strings.HasPrefix(p, base+"/") {
			return fmt.Errorf("静态目录超出文件根目录")
		}
	default:
		return fmt.Errorf("站点类型无效")
	}
	return nil
}
