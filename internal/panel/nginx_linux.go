package panel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var nginxMu sync.Mutex

func initNginx() error {
	for _, d := range []string{"sites", "logs", "backups", "staging", "challenges"} {
		if e := os.MkdirAll(filepath.Join(cfg.nginxPrefix, d), 0755); e != nil {
			return e
		}
	}
	p := filepath.Join(cfg.nginxPrefix, "nginx.conf")
	if _, e := os.Stat(p); os.IsNotExist(e) {
		s := fmt.Sprintf("user www-data;\nworker_processes auto;\npid %s;\nerror_log %s;\nevents { worker_connections 1024; }\nhttp {\n map $http_upgrade $connection_upgrade { default upgrade; '' close; }\n include %s;\n default_type application/octet-stream;\n sendfile on;\n access_log %s;\n include %s;\n}\n", q(filepath.Join(cfg.nginxPrefix, "nginx.pid")), q(filepath.Join(cfg.nginxPrefix, "logs/error.log")), q(cfg.nginxMimeTypes), q(filepath.Join(cfg.nginxPrefix, "logs/access.log")), q(filepath.Join(cfg.nginxPrefix, "sites/*/server.conf")))
		return os.WriteFile(p, []byte(s), 0644)
	}
	return nil
}

func ng(ctx context.Context, args ...string) (string, error) {
	return command(ctx, cfg.nginxBinary, append([]string{"-p", cfg.nginxPrefix + "/", "-c", filepath.Join(cfg.nginxPrefix, "nginx.conf")}, args...)...)
}
func nginxRunning() bool {
	b, e := os.ReadFile(filepath.Join(cfg.nginxPrefix, "nginx.pid"))
	if e != nil {
		return false
	}
	pid, e := strconv.Atoi(strings.TrimSpace(string(b)))
	return e == nil && pid > 1 && syscall.Kill(pid, 0) == nil
}

func validateSite(s site) error {
	if !validName(s.ID) {
		return fmt.Errorf("站点 ID 仅支持字母、数字、点、横线和下划线")
	}
	if err := validateSiteProtocols(s); err != nil {
		return err
	}
	domains := strings.Fields(s.Domains)
	if len(domains) == 0 {
		return fmt.Errorf("请输入域名")
	}
	for _, d := range domains {
		if !validName(strings.TrimPrefix(d, "*.")) || strings.HasPrefix(d, "-") {
			return fmt.Errorf("域名无效: %s", d)
		}
	}
	if s.Kind == "locations" {
		if strings.ContainsRune(s.Routing, 0) {
			return fmt.Errorf("路由配置包含无效字符")
		}
	} else if s.Kind == "proxy" {
		u, e := url.Parse(s.Upstream)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || strings.ContainsAny(s.Upstream, "\r\n\t ;{}\"\\$") {
			return fmt.Errorf("上游地址无效")
		}
	} else if s.Kind == "static" {
		p, e := filepath.Abs(s.Root)
		if e != nil {
			return e
		}
		st, e := os.Stat(p)
		if e != nil || !st.IsDir() {
			return fmt.Errorf("静态目录不存在")
		}
	} else {
		return fmt.Errorf("站点类型无效")
	}
	return nil
}
func renderSite(s site) map[string][]byte { return renderSiteAt(s, cfg.nginxPrefix) }
func applySite(ctx context.Context, s site) (string, error) {
	s.Manual = false
	if s.QUIC {
		version, err := ng(ctx, "-V")
		if err != nil || !strings.Contains(version, "--with-http_v3_module") {
			return "", fmt.Errorf("当前 Nginx 未编译 HTTP/3 模块，无法启用 QUIC")
		}
	}
	if err := normalizeSiteBindings(&s); err != nil {
		return "", err
	}
	if e := validateSite(s); e != nil {
		return "", e
	}
	dir := filepath.Join(cfg.nginxPrefix, "sites", s.ID)
	stage, e := os.MkdirTemp(filepath.Join(cfg.nginxPrefix, "staging"), s.ID+"-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(stage)
	files := renderSite(s)
	if raw, err := os.ReadFile(filepath.Join(dir, "site.json")); err == nil {
		var previous site
		if err = json.Unmarshal(raw, &previous); err != nil {
			return "", err
		}
		current := map[string]string{}
		for _, name := range []string{"server.conf", "routing.conf", "tls.conf", "custom.conf"} {
			data, readErr := os.ReadFile(filepath.Join(dir, name))
			if readErr != nil {
				return "", readErr
			}
			current[name] = string(data)
		}
		if err = patchSiteFiles(previous, cfg.nginxPrefix, current, files); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	for _, pair := range siteCertificateNames(s) {
		for _, name := range pair[:2] {
			if _, ok := files[name]; !ok {
				data, err := os.ReadFile(filepath.Join(dir, name))
				if os.IsNotExist(err) {
					data, err = os.ReadFile(filepath.Join(dir, legacyCertificateName(name)))
				}
				if err == nil {
					files[name] = data
				}
			}
		}
		if s.TLS {
			if len(files[pair[0]]) == 0 || len(files[pair[1]]) == 0 {
				return "", fmt.Errorf("域名 %s 需要证书和私钥", pair[2])
			}
			if err := validateCertificateData(files[pair[0]], files[pair[1]], pair[2]); err != nil {
				return "", err
			}
		}
	}
	for name, b := range files {
		mode := os.FileMode(0644)
		if name == "private.key" || strings.HasPrefix(name, "private-") {
			mode = 0600
		}
		if e = os.WriteFile(filepath.Join(stage, name), b, mode); e != nil {
			return "", e
		}
	}
	backup := filepath.Join(cfg.nginxPrefix, "backups", s.ID+"-"+time.Now().Format("20060102-150405")+"-"+token()[:8])
	existed := false
	if _, e = os.Stat(dir); e == nil {
		if e = os.Rename(dir, backup); e != nil {
			return "", e
		}
		existed = true
	}
	restore := func() {
		_ = os.RemoveAll(dir)
		if existed {
			_ = os.Rename(backup, dir)
		}
	}
	if e = os.Rename(stage, dir); e != nil {
		restore()
		return "", e
	}
	out, e := ng(ctx, "-t")
	if e != nil {
		restore()
		return out, e
	}
	if nginxRunning() {
		out, e = ng(ctx, "-s", "reload")
		if e != nil {
			restore()
			_, _ = ng(ctx, "-s", "reload")
			return out, e
		}
	}
	audit("nginx_site_apply", s.ID)
	return out, nil
}
func registerNginx(mux *http.ServeMux) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		serveExistingNginx(w, r.WithContext(ctx), func(ctx context.Context, _ bool, script string, args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				return string(out), fmt.Errorf("%v: %s", err, stderr.String())
			}
			return string(out), nil
		}, cfg.nginxPrefix, cfg.nginxBinary)
	}
	mux.HandleFunc("/api/nginx/existing", handler)
	mux.HandleFunc("/api/nginx/existing/", handler)
	registerSiteVersions(mux)
	registerSiteACME(mux)
	mux.HandleFunc("POST /api/nginx/sites/{id}/config", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !validName(id) {
			fail(w, 400, fmt.Errorf("站点 ID 无效"))
			return
		}
		var b struct {
			File     string `json:"file"`
			Content  string `json:"content"`
			Revision string `json:"revision"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		if b.File != "server.conf" && b.File != "routing.conf" && b.File != "tls.conf" && b.File != "custom.conf" {
			fail(w, 400, fmt.Errorf("配置文件无效"))
			return
		}
		nginxMu.Lock()
		defer nginxMu.Unlock()
		dir := filepath.Join(cfg.nginxPrefix, "sites", id)
		chunks := []string{}
		for _, name := range []string{"server.conf", "routing.conf", "tls.conf", "custom.conf"} {
			v, e := os.ReadFile(filepath.Join(dir, name))
			if e != nil {
				fail(w, 400, e)
				return
			}
			chunks = append(chunks, string(v))
		}
		if b.Revision != digest([]byte(strings.Join(chunks, "\x00"))) {
			fail(w, 409, fmt.Errorf("配置已被修改，请重新打开"))
			return
		}
		if b.File == "server.conf" {
			content, e := replaceMarkedBlocks(b.Content, certStart, certEnd, fmt.Sprintf(" location ^~ /.well-known/acme-challenge/ { root %s; }", q(filepath.Join(cfg.nginxPrefix, "challenges"))))
			if e != nil {
				fail(w, 400, e)
				return
			}
			b.Content = content
		}
		stage, e := os.MkdirTemp(filepath.Join(cfg.nginxPrefix, "staging"), "raw-")
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer os.RemoveAll(stage)
		if e = copySiteSnapshot(dir, stage); e != nil {
			fail(w, 500, e)
			return
		}
		metadataPath := filepath.Join(stage, "site.json")
		metadata, e := os.ReadFile(metadataPath)
		if e != nil {
			fail(w, 500, e)
			return
		}
		var s site
		if e = json.Unmarshal(metadata, &s); e != nil {
			fail(w, 500, e)
			return
		}
		s.Manual = true
		metadata, _ = json.MarshalIndent(s, "", "  ")
		if e = os.WriteFile(metadataPath, metadata, 0600); e == nil {
			e = os.WriteFile(filepath.Join(stage, b.File), []byte(b.Content), 0644)
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		out, e := activateSiteSnapshot(id, stage)
		if e != nil {
			fail(w, 400, e)
			return
		}
		audit("nginx_raw_config", map[string]string{"site": id, "file": b.File})
		respond(w, map[string]string{"output": out})
	})
	mux.HandleFunc("GET /api/nginx/sites", func(w http.ResponseWriter, r *http.Request) {
		nginxMu.Lock()
		defer nginxMu.Unlock()
		entries, _ := os.ReadDir(filepath.Join(cfg.nginxPrefix, "sites"))
		sites := []map[string]any{}
		for _, d := range entries {
			if !d.IsDir() {
				continue
			}
			p := filepath.Join(cfg.nginxPrefix, "sites", d.Name())
			b, e := os.ReadFile(filepath.Join(p, "site.json"))
			var s site
			if e != nil || json.Unmarshal(b, &s) != nil {
				continue
			}
			custom, _ := os.ReadFile(filepath.Join(p, "custom.conf"))
			s.Custom = string(custom)
			files := map[string]string{}
			for _, name := range []string{"server.conf", "routing.conf", "tls.conf", "custom.conf"} {
				b, _ := os.ReadFile(filepath.Join(p, name))
				files[name] = string(b)
			}
			expiry := ""
			if b, e := os.ReadFile(filepath.Join(p, "certificate.pem")); e == nil {
				key, _ := os.ReadFile(filepath.Join(p, "private.key"))
				if pair, e := tls.X509KeyPair(b, key); e == nil {
					if c, e := x509.ParseCertificate(pair.Certificate[0]); e == nil {
						expiry = c.NotAfter.Format(time.RFC3339)
					}
				}
			}
			for i := range s.Bindings {
				certData, err := os.ReadFile(filepath.Join(p, "certificate-"+s.Bindings[i].ID+".pem"))
				if err == nil {
					block, _ := pem.Decode(certData)
					if block != nil {
						if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
							s.Bindings[i].CertificateExpiry = cert.NotAfter.Format(time.RFC3339)
						}
					}
				}
			}
			refreshSiteCertificateInfo(&s, func(name string) []byte { data, _ := os.ReadFile(filepath.Join(p, name)); return data })
			sites = append(sites, map[string]any{"site": s, "files": files, "revision": digest([]byte(strings.Join([]string{files["server.conf"], files["routing.conf"], files["tls.conf"], files["custom.conf"]}, "\x00"))), "certificateExpiry": expiry})
		}
		respond(w, map[string]any{"sites": sites, "running": nginxRunning(), "prefix": cfg.nginxPrefix})
	})
	mux.HandleFunc("POST /api/nginx/sites", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			PatchOnly bool   `json:"patchOnly"`
			Site      site   `json:"site"`
			Revision  string `json:"revision"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		nginxMu.Lock()
		defer nginxMu.Unlock()
		dir := filepath.Join(cfg.nginxPrefix, "sites", b.Site.ID)
		if !validName(b.Site.ID) {
			fail(w, 400, fmt.Errorf("站点 ID 无效"))
			return
		}
		if _, e := os.Stat(dir); e == nil {
			chunks := []string{}
			for _, n := range []string{"server.conf", "routing.conf", "tls.conf", "custom.conf"} {
				v, _ := os.ReadFile(filepath.Join(dir, n))
				chunks = append(chunks, string(v))
			}
			if b.Revision != digest([]byte(strings.Join(chunks, "\x00"))) {
				fail(w, 409, fmt.Errorf("配置已被修改，请刷新后重试"))
				return
			}
		} else if b.Revision != "" {
			fail(w, 409, fmt.Errorf("站点已删除，请刷新"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		out, e := applySite(ctx, b.Site)
		if e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, map[string]any{"ok": true, "output": out})
	})
	mux.HandleFunc("POST /api/nginx/control", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Action string `json:"action"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		nginxMu.Lock()
		defer nginxMu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		args := []string{}
		switch b.Action {
		case "test":
			args = []string{"-t"}
		case "start":
			if nginxRunning() {
				fail(w, 400, fmt.Errorf("已经运行"))
				return
			}
		case "reload":
			args = []string{"-s", "reload"}
		case "stop":
			args = []string{"-s", "quit"}
		default:
			fail(w, 400, fmt.Errorf("操作无效"))
			return
		}
		var out string
		var e error
		if cfg.nginxService != "" && (b.Action == "start" || b.Action == "stop") {
			out, e = command(ctx, "systemctl", b.Action, cfg.nginxService)
		} else {
			out, e = ng(ctx, args...)
		}
		if e != nil {
			fail(w, 400, e)
			return
		}
		audit("nginx_"+b.Action, nil)
		respond(w, map[string]string{"output": out})
	})
	mux.HandleFunc("DELETE /api/nginx/sites/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if !validName(id) {
			fail(w, 400, fmt.Errorf("ID 无效"))
			return
		}
		nginxMu.Lock()
		defer nginxMu.Unlock()
		src := filepath.Join(cfg.nginxPrefix, "sites", id)
		dst := filepath.Join(cfg.nginxPrefix, "backups", id+"-deleted-"+token()[:8])
		if e := os.Rename(src, dst); e != nil {
			fail(w, 400, e)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		_, e := ng(ctx, "-t")
		if e == nil && nginxRunning() {
			_, e = ng(ctx, "-s", "reload")
		}
		if e != nil {
			_ = os.Rename(dst, src)
			fail(w, 400, e)
			return
		}
		audit("nginx_delete", id)
		respond(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/nginx/logs", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		kind := r.URL.Query().Get("kind")
		if !validName(id) || (kind != "access" && kind != "error") {
			fail(w, 400, fmt.Errorf("日志参数无效"))
			return
		}
		f, e := os.Open(filepath.Join(cfg.nginxPrefix, "logs", id+"."+kind+".log"))
		if os.IsNotExist(e) {
			respond(w, map[string]string{"text": "暂无日志"})
			return
		}
		if e != nil {
			fail(w, 400, e)
			return
		}
		defer f.Close()
		respond(w, map[string]string{"text": tail(f, 300, 256<<10)})
	})
}
