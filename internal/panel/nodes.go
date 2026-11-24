package panel

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Capability struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}
type Node struct {
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Mode         string                `json:"mode"`
	Host         string                `json:"host,omitempty"`
	Port         int                   `json:"port,omitempty"`
	User         string                `json:"user,omitempty"`
	Fingerprint  string                `json:"fingerprint,omitempty"`
	FileRoot     string                `json:"fileRoot"`
	Socket       string                `json:"socket,omitempty"`
	Sudo         bool                  `json:"sudo"`
	SudoMode     string                `json:"sudoMode,omitempty"`
	Online       bool                  `json:"online"`
	LastSeen     string                `json:"lastSeen,omitempty"`
	LastError    string                `json:"lastError,omitempty"`
	Capabilities map[string]Capability `json:"capabilities"`
}
type SSHSecret struct {
	SudoPassword string `json:"sudoPassword,omitempty"`
	Password     string `json:"password,omitempty"`
	PrivateKey   string `json:"privateKey,omitempty"`
	Passphrase   string `json:"passphrase,omitempty"`
}
type nodeInput struct {
	Name        string    `json:"name"`
	Mode        string    `json:"mode"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	User        string    `json:"user"`
	Fingerprint string    `json:"fingerprint"`
	FileRoot    string    `json:"fileRoot"`
	Sudo        bool      `json:"sudo"`
	SudoMode    string    `json:"sudoMode,omitempty"`
	Secret      SSHSecret `json:"secret"`
}
type nodeDriver interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
	Probe(context.Context) (map[string]Capability, error)
	Close()
}
type nodeManager struct {
	db      *sql.DB
	aead    cipher.AEAD
	mu      sync.Mutex
	drivers map[string]nodeDriver
	agents  map[string]*agentSession
	pki     *agentPKI
}

func newNodeManager() (*nodeManager, error) {
	keyPath := filepath.Join(cfg.data, "credentials.key")
	key, e := os.ReadFile(keyPath)
	if os.IsNotExist(e) {
		if _, dbErr := os.Stat(filepath.Join(cfg.data, "control.db")); dbErr == nil {
			return nil, fmt.Errorf("credentials.key 丢失，拒绝创建新密钥以免损坏已有凭证")
		}
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		e = os.WriteFile(keyPath, key, 0600)
	}
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("sqlite", filepath.Join(cfg.data, "control.db"))
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS nodes(id TEXT PRIMARY KEY, data TEXT NOT NULL, secret BLOB);
 CREATE TABLE IF NOT EXISTS enrollments(hash TEXT PRIMARY KEY,node_id TEXT NOT NULL,expires INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS metrics_history(node_id TEXT NOT NULL, sampled_at INTEGER NOT NULL, data TEXT NOT NULL, PRIMARY KEY(node_id,sampled_at));
 CREATE TABLE IF NOT EXISTS scan_jobs(id TEXT PRIMARY KEY,node_id TEXT NOT NULL,path TEXT NOT NULL,status TEXT NOT NULL,created INTEGER NOT NULL,result TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '');
 CREATE TABLE IF NOT EXISTS systemd_preferences(node_id TEXT NOT NULL,unit TEXT NOT NULL,data TEXT NOT NULL,PRIMARY KEY(node_id,unit));
 CREATE TABLE IF NOT EXISTS systemd_categories(node_id TEXT PRIMARY KEY,data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS nginx_notes(node_id TEXT NOT NULL,site_key TEXT NOT NULL,note TEXT NOT NULL,PRIMARY KEY(node_id,site_key));
 CREATE TABLE IF NOT EXISTS nginx_categories(node_id TEXT PRIMARY KEY,data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS certificates(node_id TEXT NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,certificate TEXT NOT NULL,secret BLOB NOT NULL,fingerprint TEXT NOT NULL,PRIMARY KEY(node_id,id),UNIQUE(node_id,fingerprint));
	CREATE TABLE IF NOT EXISTS agent_devices(node_id TEXT PRIMARY KEY,serial TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS agent_renewals(node_id TEXT PRIMARY KEY,old_serial TEXT NOT NULL,new_serial TEXT NOT NULL,certificate TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS audit(id INTEGER PRIMARY KEY,time TEXT,node_id TEXT,action TEXT,detail TEXT);`)
	if e != nil {
		db.Close()
		return nil, e
	}
	m := &nodeManager{db: db, aead: aead, drivers: map[string]nodeDriver{}, agents: map[string]*agentSession{}}
	if cfg.socket != "" {
		n := Node{ID: "local", Name: "本机 Agent", Mode: "local", Socket: cfg.socket, FileRoot: "/"}
		b, _ := json.Marshal(n)
		if _, e = db.Exec("INSERT OR IGNORE INTO nodes(id,data) VALUES(?,?)", n.ID, string(b)); e != nil {
			return nil, e
		}
	}
	m.pki, e = newAgentPKI()
	if e != nil {
		return nil, e
	}
	return m, nil
}
func (m *nodeManager) encrypt(id string, s SSHSecret) ([]byte, error) {
	b, e := json.Marshal(s)
	if e != nil {
		return nil, e
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	return m.aead.Seal(nonce, nonce, b, []byte(id)), nil
}
func (m *nodeManager) secret(id string) (SSHSecret, error) {
	var blob []byte
	var s SSHSecret
	if e := m.db.QueryRow("SELECT secret FROM nodes WHERE id=?", id).Scan(&blob); e != nil {
		return s, e
	}
	return m.decryptSSHSecret(id, blob)
}
func (m *nodeManager) decryptSSHSecret(id string, blob []byte) (SSHSecret, error) {
	var s SSHSecret
	n := m.aead.NonceSize()
	if len(blob) < n {
		return s, fmt.Errorf("凭证不存在")
	}
	b, e := m.aead.Open(nil, blob[:n], blob[n:], []byte(id))
	if e != nil {
		return s, fmt.Errorf("凭证解密失败")
	}
	e = json.Unmarshal(b, &s)
	return s, e
}
func (m *nodeManager) get(id string) (Node, error) {
	var n Node
	var b string
	e := m.db.QueryRow("SELECT data FROM nodes WHERE id=?", id).Scan(&b)
	if e == nil {
		e = json.Unmarshal([]byte(b), &n)
	}
	return n, e
}
func (m *nodeManager) list() ([]Node, error) {
	rows, e := m.db.Query("SELECT data FROM nodes ORDER BY id")
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	nodes := []Node{}
	for rows.Next() {
		var b string
		var n Node
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal([]byte(b), &n); e != nil {
			return nil, e
		}
		n.FileRoot = "/"
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}
func (m *nodeManager) save(n Node) error {
	b, e := json.Marshal(n)
	if e != nil {
		return e
	}
	_, e = m.db.Exec("UPDATE nodes SET data=? WHERE id=?", string(b), n.ID)
	return e
}
func (m *nodeManager) record(id, action string, detail any) {
	b, _ := json.Marshal(detail)
	_, e := m.db.Exec("INSERT INTO audit(time,node_id,action,detail) VALUES(?,?,?,?)", time.Now().UTC().Format(time.RFC3339), id, action, string(b))
	if e != nil {
		audit("control_audit_error", e.Error())
	}
}
func (m *nodeManager) driver(n Node) (nodeDriver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d := m.drivers[n.ID]; d != nil {
		return d, nil
	}
	var d nodeDriver
	switch n.Mode {
	case "local", "agent":
		d = newAgentDriver(m, n)
	case "ssh":
		s, e := m.secret(n.ID)
		if e != nil {
			return nil, e
		}
		d = newSSHDriver(n, s)
	default:
		return nil, fmt.Errorf("连接模式无效")
	}
	m.drivers[n.ID] = d
	return d, nil
}
func (m *nodeManager) probe(ctx context.Context, n Node) Node {
	var original string
	var originalCredential []byte
	if err := m.db.QueryRow("SELECT data,secret FROM nodes WHERE id=?", n.ID).Scan(&original, &originalCredential); err != nil {
		n.Online = false
		n.LastError = "主机已删除"
		return n
	}
	if err := json.Unmarshal([]byte(original), &n); err != nil {
		n.Online = false
		return n
	}

	d, e := m.driver(n)
	var caps map[string]Capability
	if e == nil {
		caps, e = d.Probe(ctx)
	}
	n.Online = e == nil
	n.LastError = ""
	if e != nil {
		n.LastError = e.Error()
	} else {
		n.LastSeen = time.Now().UTC().Format(time.RFC3339)
		n.Capabilities = caps
	}
	data, _ := json.Marshal(n)
	result, err := m.db.Exec("UPDATE nodes SET data=? WHERE id=? AND data=? AND secret IS ?", string(data), n.ID, original, originalCredential)
	if err == nil {
		if count, _ := result.RowsAffected(); count == 0 {
			if current, err := m.get(n.ID); err == nil {
				return current
			}
		}
	}
	return n
}
func (m *nodeManager) closeNode(id string) {
	m.mu.Lock()
	d := m.drivers[id]
	delete(m.drivers, id)
	a := m.agents[id]
	delete(m.agents, id)
	m.mu.Unlock()
	if d != nil {
		d.Close()
	}
	if a != nil {
		a.session.Close()
	}
}
func (m *nodeManager) background() {
	go func() {
		for {
			nodes, e := m.list()
			if e == nil {
				sem := make(chan struct{}, 4)
				var wg sync.WaitGroup
				for _, n := range nodes {
					sem <- struct{}{}
					wg.Add(1)
					go func(n Node) {
						defer wg.Done()
						defer func() { <-sem }()
						ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
						defer cancel()
						checked := m.probe(ctx, n)
						if checked.Online && checked.Capabilities["metrics"].Supported {
							sampleCtx, stop := context.WithTimeout(context.Background(), 8*time.Second)
							defer stop()
							if err := m.collectMetrics(sampleCtx, checked); err != nil {
								log.Printf("metrics for %s: %v", checked.ID, err)
							}
						}
					}(n)
				}
				wg.Wait()
				if _, err := m.db.Exec("DELETE FROM metrics_history WHERE sampled_at < ? OR node_id NOT IN (SELECT id FROM nodes)", time.Now().Add(-7*24*time.Hour).UnixMilli()); err != nil {
					log.Printf("prune metrics: %v", err)
				}
				if _, err := m.db.Exec("DELETE FROM scan_jobs WHERE status IN ('done','failed') AND created < ?", time.Now().Add(-7*24*time.Hour).UnixMilli()); err != nil {
					log.Printf("prune scan jobs: %v", err)
				}
			}
			time.Sleep(20 * time.Second)
		}
	}()
}

func (m *nodeManager) handleControl(w http.ResponseWriter, r *http.Request) bool {
	if m.handleScanJobs(w, r) {
		return true
	}
	if r.Method == http.MethodGet {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
		if len(parts) == 4 && parts[0] == "nodes" && parts[2] == "metrics" && parts[3] == "history" {
			m.serveMetricsHistory(w, r, parts[1])
			return true
		}
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/")
	if p == "audit" && r.Method == "GET" {
		rows, e := m.db.Query("SELECT time,node_id,action,detail FROM audit WHERE node_id=? ORDER BY id DESC LIMIT 300", r.URL.Query().Get("nodeId"))
		if e != nil {
			fail(w, 500, e)
			return true
		}
		defer rows.Close()
		entries := []map[string]string{}
		for rows.Next() {
			var t, id, a, d string
			if e = rows.Scan(&t, &id, &a, &d); e != nil {
				fail(w, 500, e)
				return true
			}
			entries = append(entries, map[string]string{"time": t, "nodeId": id, "action": a, "detail": d})
		}
		respond(w, map[string]any{"entries": entries})
		return true
	}
	if p == "nodes/probe-ssh" && r.Method == "POST" {
		var b struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return true
		}
		fingerprint, e := probeSSHHost(r.Context(), b.Host, b.Port)
		if e != nil {
			fail(w, 400, e)
		} else {
			respond(w, map[string]string{"fingerprint": fingerprint})
		}
		return true
	}
	if p == "nodes" {
		switch r.Method {
		case "GET":
			nodes, e := m.list()
			if e != nil {
				fail(w, 500, e)
			} else {
				respond(w, map[string]any{"nodes": nodes})
			}
		case "POST":
			var b nodeInput
			if e := decode(w, r, &b); e != nil {
				fail(w, 400, e)
				break
			}
			if strings.TrimSpace(b.Name) == "" || len(b.Name) > 100 {
				fail(w, 400, fmt.Errorf("请输入主机名称"))
				break
			}
			if b.Mode != "agent" && b.Mode != "ssh" {
				fail(w, 400, fmt.Errorf("选择 Agent 或 SSH 模式"))
				break
			}
			if b.FileRoot == "" {
				b.FileRoot = "/"
			}
			if !strings.HasPrefix(b.FileRoot, "/") || strings.ContainsAny(b.FileRoot, "\x00\r\n") {
				fail(w, 400, fmt.Errorf("文件根目录无效"))
				break
			}
			n := Node{ID: token()[:24], Name: b.Name, Mode: b.Mode, Host: b.Host, Port: b.Port, User: b.User, Fingerprint: b.Fingerprint, FileRoot: "/", Sudo: b.Sudo, SudoMode: b.SudoMode}
			if n.SudoMode != "" {
				n.Sudo = n.SudoMode != "none"
			}
			var encrypted []byte
			var e error
			if n.Mode == "ssh" {
				if e = validateSSH(n, b.Secret); e == nil {
					encrypted, e = m.encrypt(n.ID, b.Secret)
				}
				if e != nil {
					fail(w, 400, e)
					break
				}
			}
			if n.Mode == "ssh" && n.SudoMode == "password" {
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				candidate := newSSHDriver(n, b.Secret)
				caps, err := candidate.Probe(ctx)
				candidate.Close()
				cancel()
				if err != nil || !caps["terminal.root"].Supported {
					fail(w, 400, fmt.Errorf("SSH 或 sudo 密码验证失败，请检查凭证及账号 sudo 权限"))
					break
				}
			}
			data, _ := json.Marshal(n)
			if _, e = m.db.Exec("INSERT INTO nodes(id,data,secret) VALUES(?,?,?)", n.ID, string(data), encrypted); e != nil {
				fail(w, 500, e)
				break
			}
			m.record(n.ID, "node_created", map[string]string{"mode": n.Mode, "name": n.Name})
			result := map[string]any{"node": n}
			if n.Mode == "agent" {
				enrollment, e := m.createEnrollment(n.ID)
				if e != nil {
					fail(w, 500, e)
					break
				}
				result["enrollment"] = enrollment
			}
			respond(w, result)
		default:
			w.WriteHeader(405)
		}
		return true
	}
	parts := strings.Split(p, "/")
	if len(parts) >= 2 && parts[0] == "nodes" {
		id := parts[1]
		n, e := m.get(id)
		if e != nil {
			fail(w, 404, fmt.Errorf("主机不存在"))
			return true
		}
		if len(parts) == 2 {
			switch r.Method {
			case "GET":
				respond(w, n)
			case "DELETE":
				if n.Mode == "local" {
					fail(w, 400, fmt.Errorf("内置本机入口不能删除；独立部署时可不配置本机 socket"))
					break
				}
				_, e = m.db.Exec("DELETE FROM nodes WHERE id=?", id)
				if e == nil {
					_, _ = m.db.Exec("DELETE FROM enrollments WHERE node_id=?", id)
					_, _ = m.db.Exec("DELETE FROM agent_devices WHERE node_id=?", id)
					_, _ = m.db.Exec("DELETE FROM agent_renewals WHERE node_id=?", id)
					_, _ = m.db.Exec("DELETE FROM systemd_preferences WHERE node_id=? OR substr(node_id,1,length(?)+1)=? || ':'", id, id, id)
					_, _ = m.db.Exec("DELETE FROM systemd_categories WHERE node_id=? OR substr(node_id,1,length(?)+1)=? || ':'", id, id, id)
					_, _ = m.db.Exec("DELETE FROM nginx_notes WHERE node_id=?", id)
					_, _ = m.db.Exec("DELETE FROM nginx_categories WHERE node_id=?", id)
					_, _ = m.db.Exec("DELETE FROM certificates WHERE node_id=?", id)
					m.closeNode(id)
					m.record(id, "node_deleted", nil)
					respond(w, map[string]bool{"ok": true})
				} else {
					fail(w, 500, e)
				}
			default:
				w.WriteHeader(405)
			}
			return true
		}
		if len(parts) == 4 && parts[2] == "systemd" && parts[3] == "categories" {
			if scope, ok := systemdPreferenceScope(w, r, id); ok {
				m.systemdCategories(w, r, scope)
			}
			return true
		}
		if len(parts) == 3 && parts[2] == "install-agent" {
			m.installAgentSSH(w, r, n)
			return true
		}
		if len(parts) == 4 && parts[2] == "systemd" && parts[3] == "preferences" {
			if scope, ok := systemdPreferenceScope(w, r, id); ok {
				m.systemdPreferences(w, r, scope)
			}
			return true
		}
		if len(parts) == 4 && parts[2] == "nginx" && parts[3] == "notes" {
			m.nginxNotes(w, r, id)
			return true
		}
		if len(parts) >= 3 && len(parts) <= 5 && parts[2] == "certificates" {
			certificateID, operation := "", ""
			if len(parts) >= 4 {
				if parts[3] == "preview" {
					operation = "preview"
				} else {
					certificateID = parts[3]
				}
			}
			if len(parts) == 5 {
				operation = parts[4]
			}
			m.certificates(w, r, id, certificateID, operation)
			return true
		}
		if len(parts) == 4 && parts[2] == "nginx" && parts[3] == "categories" {
			m.nginxCategories(w, r, id)
			return true
		}
		if len(parts) == 3 && parts[2] == "probe" && r.Method == "POST" {
			ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
			defer cancel()
			respond(w, m.probe(ctx, n))
			return true
		}
		if len(parts) == 3 && parts[2] == "sudo" && r.Method == http.MethodPost {
			m.updateSudo(w, r, n)
			return true
		}
		if len(parts) == 3 && parts[2] == "ssh-credentials" && r.Method == "POST" {
			m.rotateSSHCredentials(w, r, n)
			return true
		}
		if len(parts) == 3 && parts[2] == "enrollment" && r.Method == "POST" {
			if n.Mode != "agent" {
				fail(w, 400, fmt.Errorf("仅 Agent 主机支持注册"))
			} else {
				v, e := m.createEnrollment(id)
				if e != nil {
					fail(w, 500, e)
				} else {
					respond(w, v)
				}
			}
			return true
		}
	}
	return false
}
func (m *nodeManager) route(w http.ResponseWriter, r *http.Request) {
	id := "local"
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if len(parts) >= 3 && parts[0] == "nodes" {
		id = parts[1]
		r.URL.Path = "/api/" + strings.Join(parts[2:], "/")
	}
	n, e := m.get(id)
	if e != nil {
		fail(w, 404, fmt.Errorf("主机不存在"))
		return
	}
	d, e := m.driver(n)
	if e != nil {
		fail(w, 502, e)
		return
	}
	if r.Method != "GET" || r.URL.Path == "/api/terminal" {
		m.record(id, r.Method+" "+r.URL.Path, nil)
	}
	d.ServeHTTP(w, r)
}

type agentDriver struct {
	m         *nodeManager
	n         Node
	proxy     *httputil.ReverseProxy
	transport *http.Transport
}

func newAgentDriver(m *nodeManager, n Node) *agentDriver {
	d := &agentDriver{m: m, n: n}
	d.transport = &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if n.Mode == "local" {
			return (&net.Dialer{}).DialContext(ctx, "unix", n.Socket)
		}
		m.mu.Lock()
		a := m.agents[n.ID]
		m.mu.Unlock()
		if a == nil || a.session.IsClosed() {
			return nil, fmt.Errorf("Agent 离线")
		}
		type result struct {
			c net.Conn
			e error
		}
		ch := make(chan result)
		go func() {
			c, e := a.session.Open()
			select {
			case ch <- result{c, e}:
			case <-ctx.Done():
				if c != nil {
					c.Close()
				}
			}
		}()
		select {
		case v := <-ch:
			return v.c, v.e
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, ResponseHeaderTimeout: 30 * time.Second, MaxIdleConnsPerHost: 8}
	d.proxy = &httputil.ReverseProxy{Director: func(r *http.Request) {
		r.URL.Scheme = "http"
		r.URL.Host = "agent"
		r.Host = "agent"
		for k := range r.Header {
			if strings.HasPrefix(strings.ToLower(k), "x-gateway-") || strings.HasPrefix(strings.ToLower(k), "x-ownpanel-") {
				r.Header.Del(k)
			}
		}
		r.Header.Del("Authorization")
		r.Header.Del("Proxy-Authorization")
		r.Header.Del("Cookie")
		if id := panelUser(r.Context()); id > 0 {
			r.URL.Path = "/gateway/v1" + r.URL.Path
			r.URL.RawPath = ""
			r.Header.Set("X-OwnPanel-User", strconv.FormatInt(id, 10))
		}
		r.Header.Del("X-CSRF-Token")
	}, Transport: d.transport, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		fail(w, 502, fmt.Errorf("主机 %s 连接失败: %v", n.Name, e))
	}}
	return d
}
func (d *agentDriver) ServeHTTP(w http.ResponseWriter, r *http.Request) { d.proxy.ServeHTTP(w, r) }
func (d *agentDriver) Close()                                           { d.transport.CloseIdleConnections() }
func (d *agentDriver) Probe(ctx context.Context) (map[string]Capability, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://agent/api/status", nil)
	res, e := d.transport.RoundTrip(req)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Agent 返回 %d", res.StatusCode)
	}
	var s map[string]any
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&s); e != nil {
		return nil, e
	}
	caps := map[string]Capability{}
	for _, k := range []string{"metrics", "files.read", "files.write", "terminal", "terminal.root", "nginx.read", "nginx.write", "logs.ssh", "logs.audit", "firewall.read", "firewall.write"} {
		caps[k] = Capability{Supported: true}
	}
	if s["ufwAvailable"] != true {
		caps["firewall.read"] = Capability{Reason: "未安装 UFW"}
		caps["firewall.write"] = caps["firewall.read"]
	}
	available := s["iptablesAvailable"] == true
	caps["iptables.read"] = Capability{Supported: available, Reason: func() string {
		if available {
			return ""
		}
		return "未安装 iptables"
	}()}
	caps["iptables.write"] = caps["iptables.read"]
	caps["systemd.read"] = Capability{Supported: s["systemdAvailable"] == true, Reason: "需要 systemd"}
	caps["systemd.write"] = caps["systemd.read"]
	return caps, nil
}
