package panel

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"

	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type config struct {
	mode, listen, socket, data, web, fileRoot, nginxPrefix, nginxService, nginxBinary, nginxMimeTypes string
	socketGID                                                                                         int
	agentListen, agentAdvertise, agentHosts, agentURL, enrollmentFile                                 string
}

var cfg = config{nginxBinary: "nginx", nginxMimeTypes: "/etc/nginx/mime.types"}
var auditMu sync.Mutex
var panelCert, panelKey string
var certificateLineages string

func Run(mode string) {
	defaultMode := mode
	if defaultMode == "" {
		defaultMode = "server"
	}
	flag.StringVar(&cfg.mode, "mode", defaultMode, "server or agent")
	flag.StringVar(&cfg.listen, "listen", "127.0.0.1:7080", "HTTP bind address")
	flag.StringVar(&panelCert, "tls-cert", "", "management HTTPS certificate chain")
	flag.StringVar(&panelKey, "tls-key", "", "management HTTPS private key")
	flag.StringVar(&certificateLineages, "certificate-lineages", "", "agent JSON mapping of TLS sites to local Certbot lineage directories")
	defaultSocket := "/run/ownpanel/agent.sock"
	if mode == "server" {
		defaultSocket = ""
	}
	flag.StringVar(&cfg.socket, "socket", defaultSocket, "optional local agent Unix socket; empty for remote-only center")
	flag.StringVar(&cfg.data, "data", "/var/lib/ownpanel", "private state directory")
	flag.StringVar(&cfg.web, "web", "web/dist", "built frontend")
	flag.StringVar(&cfg.fileRoot, "file-root", "/", "legacy compatibility flag; agent file management uses /")
	flag.StringVar(&cfg.nginxPrefix, "nginx-prefix", "/etc/nginx/ownpanel", "isolated nginx prefix")
	flag.StringVar(&cfg.nginxBinary, "nginx-binary", "nginx", "dedicated nginx executable")
	flag.StringVar(&cfg.nginxMimeTypes, "nginx-mime-types", "/etc/nginx/mime.types", "dedicated nginx MIME types file")
	flag.StringVar(&cfg.nginxService, "nginx-service", "", "optional dedicated nginx systemd unit")
	flag.IntVar(&cfg.socketGID, "socket-gid", -1, "trusted panel process group")
	flag.StringVar(&cfg.agentListen, "agent-listen", "127.0.0.1:7443", "agent enrollment and tunnel TLS listener; empty disables")
	flag.StringVar(&cfg.agentAdvertise, "agent-advertise", "https://localhost:7443", "URL reachable by remote agents")
	flag.StringVar(&cfg.agentHosts, "agent-hosts", "localhost,127.0.0.1", "comma-separated server certificate DNS names/IPs")
	flag.StringVar(&cfg.agentURL, "connect", "", "agent outbound center URL")
	flag.StringVar(&cfg.enrollmentFile, "enrollment-file", "", "one-time JSON enrollment file")
	flag.Parse()
	if mode != "" && cfg.mode != mode {
		log.Fatal("this binary only supports mode ", mode)
	}
	if err := os.MkdirAll(cfg.data, 0700); err != nil {
		log.Fatal(err)
	}
	if cfg.mode == "agent" {
		if os.Geteuid() != 0 {
			log.Fatal("agent requires root")
		}
		runAgent()
		return
	}
	if cfg.mode != "server" {
		log.Fatal("invalid mode")
	}
	runServer()
}
func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, code int, e error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func audit(action string, v any) {
	auditMu.Lock()
	defer auditMu.Unlock()
	f, e := os.OpenFile(filepath.Join(cfg.data, "audit.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		log.Print(e)
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(map[string]any{"time": time.Now().UTC(), "action": action, "detail": v})
}

type session struct {
	userID  int64
	name    string
	csrf    string
	expires time.Time
}

func runServer() {
	access, err := loadPublicAccess()
	if err != nil {
		log.Fatal(err)
	}
	gatewayMode := access.gateway != ""
	cookieName := "ownpanel"
	if access.origin != "" {
		cookieName = "__Host-ownpanel"
	}
	if (panelCert == "") != (panelKey == "") {
		log.Fatal("configure both -tls-cert and -tls-key")
	}
	if panelCert != "" {
		if _, err := tls.LoadX509KeyPair(panelCert, panelKey); err != nil {
			log.Fatal(err)
		}
	}
	hashPath := filepath.Join(cfg.data, "password.hash")
	hash, e := os.ReadFile(hashPath)
	if os.IsNotExist(e) {
		password := os.Getenv("OWNPANEL_PASSWORD")
		generated := password == "" || gatewayMode
		if generated {
			password = token()[:24]
		}
		if len(password) < 12 {
			log.Fatal("OWNPANEL_PASSWORD requires at least 12 characters")
		}
		hash, e = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if e != nil {
			log.Fatal(e)
		}
		if e = os.WriteFile(hashPath, hash, 0600); e != nil {
			log.Fatal(e)
		}
		if generated && !gatewayMode {
			p := filepath.Join(cfg.data, "initial-password")
			if e = os.WriteFile(p, []byte(password+"\n"), 0600); e != nil {
				log.Fatal(e)
			}
			log.Printf("Initial admin password stored at %s", p)
		}
	} else if e != nil {
		log.Fatal(e)
	}
	var mu sync.Mutex
	var passwordMu sync.Mutex
	sessions := map[string]session{}
	type terminalTicket struct {
		expires       time.Time
		node, session string
	}
	tickets := map[string]terminalTicket{}
	active := map[string]map[net.Conn]string{}
	revoke := func(id string) {
		mu.Lock()
		delete(sessions, id)
		connections := active[id]
		delete(active, id)
		mu.Unlock()
		for conn := range connections {
			_ = conn.Close()
		}
	}
	nodes, e := newNodeManager()
	if e != nil {
		log.Fatal(e)
	}
	nodes.background()
	if err := nodes.startScanJobs(); err != nil {
		log.Fatal(err)
	}
	go nodes.listenAgents()
	limits := loginLimiter{entries: map[string]loginAttempts{}}
	loginWorkers := make(chan struct{}, 4)
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", readinessHandler(nodes.db, cfg.web))
	issue := func(w http.ResponseWriter, r *http.Request, userID int64, name string) session {
		lifetime := 8 * time.Hour
		if gatewayMode {
			lifetime = time.Hour
		}
		s := session{csrf: token(), expires: time.Now().Add(lifetime), userID: userID, name: name}
		id := token()
		mu.Lock()
		for k, v := range sessions {
			if time.Now().After(v.expires) {
				delete(sessions, k)
			}
		}
		sessions[id] = s
		mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil || access.origin != "", SameSite: http.SameSiteStrictMode, MaxAge: int(lifetime.Seconds())})
		audit("login", map[string]any{"userId": userID, "source": access.source(r)})
		return s
	}
	if gatewayMode {
		access.register(mux, func(w http.ResponseWriter, r *http.Request, id int64, name string) { issue(w, r, id, name) })
	}
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		if !access.originOK(r) {
			fail(w, 403, fmt.Errorf("来源不匹配"))
			return
		}
		if gatewayMode {
			fail(w, 403, fmt.Errorf("请使用网关登录"))
			return
		}
		host := access.source(r)
		if !limits.allow(host, time.Now()) {
			fail(w, 429, fmt.Errorf("尝试次数过多，请 15 分钟后重试"))
			return
		}
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(5 * time.Second))
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		err := decode(w, r, &body)
		_ = controller.SetReadDeadline(time.Time{})
		if err != nil {
			fail(w, 400, fmt.Errorf("请求无效"))
			return
		}
		select {
		case loginWorkers <- struct{}{}:
			defer func() { <-loginWorkers }()
		default:
			w.Header().Set("Retry-After", "1")
			fail(w, 429, fmt.Errorf("登录请求过多，请稍后重试"))
			return
		}
		passwordMu.Lock()
		defer passwordMu.Unlock()
		if body.Username != "admin" || bcrypt.CompareHashAndPassword(hash, []byte(body.Password)) != nil {
			audit("login_failed", host)
			fail(w, 401, fmt.Errorf("用户名或密码错误"))
			return
		}
		limits.clear(host)
		s := issue(w, r, 0, "admin")
		respond(w, map[string]any{"csrf": s.csrf, "username": s.name})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		c, e := r.Cookie(cookieName)
		if e != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			respond(w, map[string]any{"error": "请先登录", "gateway": gatewayMode})
			return
		}
		mu.Lock()
		s, ok := sessions[c.Value]
		mu.Unlock()
		if !ok || time.Now().After(s.expires) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			respond(w, map[string]any{"error": "登录已过期", "gateway": gatewayMode})
			return
		}
		if !access.originOK(r) {
			fail(w, 403, fmt.Errorf("来源不匹配"))
			return
		}
		if r.Method != "GET" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.csrf)) != 1 {
			fail(w, 403, fmt.Errorf("CSRF 校验失败"))
			return
		}
		if gatewayMode && r.URL.Path != "/api/logout" {
			if !access.allowed(r.Context(), s.userID) {
				if r.Context().Err() != nil {
					return
				}
				revoke(c.Value)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(403)
				respond(w, map[string]any{"error": "网关未授权或不可用", "gateway": true})
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), panelUserKey{}, s.userID))
			if r.Method != "GET" || strings.HasSuffix(r.URL.Path, "/terminal") {
				audit("authorized_request", map[string]any{"userId": s.userID, "method": r.Method, "path": r.URL.Path})
			}
		}
		if nodes.handleControl(w, r) {
			return
		}
		nodeID := "local"
		endpoint := r.URL.Path
		parts := strings.Split(strings.TrimPrefix(endpoint, "/api/"), "/")
		if len(parts) >= 3 && parts[0] == "nodes" {
			nodeID = parts[1]
			endpoint = "/api/" + strings.Join(parts[2:], "/")
		}
		switch endpoint {
		case "/api/session":
			respond(w, map[string]any{"username": s.name, "userId": s.userID, "csrf": s.csrf, "gateway": gatewayMode})
			return
		case "/api/account/password":
			if gatewayMode {
				fail(w, 403, fmt.Errorf("请在网关管理账户"))
				return
			}
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var body struct {
				Current string `json:"current"`
				New     string `json:"new"`
			}
			if err := decode(w, r, &body); err != nil {
				fail(w, 400, err)
				return
			}
			if len(body.New) < 12 || len(body.New) > 72 || body.New == body.Current {
				fail(w, 400, fmt.Errorf("新密码须为 12 至 72 字节，且与当前密码不同"))
				return
			}
			passwordMu.Lock()
			defer passwordMu.Unlock()
			mu.Lock()
			current, valid := sessions[c.Value]
			mu.Unlock()
			if !valid || time.Now().After(current.expires) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(401)
				respond(w, map[string]any{"error": "登录已过期", "gateway": gatewayMode})
				return
			}
			if bcrypt.CompareHashAndPassword(hash, []byte(body.Current)) != nil {
				fail(w, 400, fmt.Errorf("当前密码错误"))
				return
			}
			replacement, err := bcrypt.GenerateFromPassword([]byte(body.New), bcrypt.DefaultCost)
			if err == nil {
				err = replacePasswordHash(hashPath, replacement)
			}
			if err != nil {
				log.Printf("replace password hash: %v", err)
				fail(w, 500, fmt.Errorf("无法保存新密码"))
				return
			}
			hash = replacement
			mu.Lock()
			clear(sessions)
			clear(tickets)
			connections := active
			active = map[string]map[net.Conn]string{}
			mu.Unlock()
			for _, group := range connections {
				for conn := range group {
					_ = conn.Close()
				}
			}
			if err := os.Remove(filepath.Join(cfg.data, "initial-password")); err != nil && !os.IsNotExist(err) {
				log.Printf("remove initial password file: %v", err)
			}
			audit("password_changed", nil)
			http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil || access.origin != "", SameSite: http.SameSiteStrictMode})
			respond(w, map[string]bool{"ok": true})
			return
		case "/api/logout":
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			revoke(c.Value)
			http.SetCookie(w, &http.Cookie{Name: cookieName, Secure: r.TLS != nil || access.origin != "", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			respond(w, map[string]bool{"ok": true})
			return
		case "/api/terminal/ticket":
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			t := token()
			mu.Lock()
			for k, v := range tickets {
				if time.Now().After(v.expires) {
					delete(tickets, k)
				}
			}
			tickets[t] = terminalTicket{time.Now().Add(30 * time.Second), nodeID, c.Value}
			mu.Unlock()
			respond(w, map[string]string{"ticket": t})
			return
		case "/api/terminal":
			t := r.URL.Query().Get("ticket")
			mu.Lock()
			ticket, exists := tickets[t]
			delete(tickets, t)
			mu.Unlock()
			if !exists || time.Now().After(ticket.expires) || ticket.node != nodeID || ticket.session != c.Value {
				fail(w, 403, fmt.Errorf("终端凭证无效"))
				return
			}
			query := r.URL.Query()
			query.Del("ticket")
			r.URL.RawQuery = query.Encode()
			audit("terminal_open", r.RemoteAddr)
		}
		if endpoint == "/api/terminal" {
			done := make(chan struct{})
			defer close(done)
			var connection net.Conn
			var expiry *time.Timer
			tracked := &socketTracker{ResponseWriter: w, onHijack: func(conn net.Conn) {
				connection = conn
				mu.Lock()
				if v, valid := sessions[c.Value]; !valid || time.Now().After(v.expires) {
					mu.Unlock()
					_ = conn.Close()
					return
				}
				if active[c.Value] == nil {
					active[c.Value] = map[net.Conn]string{}
				}
				active[c.Value][conn] = nodeID
				mu.Unlock()
				if gatewayMode {
					go func() {
						tick := time.NewTicker(30 * time.Second)
						defer tick.Stop()
						for {
							select {
							case <-done:
								return
							case <-tick.C:
								if !access.allowed(r.Context(), s.userID) {
									if r.Context().Err() != nil {
										return
									}
									revoke(c.Value)
									return
								}
							}
						}
					}()
				}
				expiry = time.AfterFunc(time.Until(s.expires), func() { _ = conn.Close() })
			}}
			defer func() {
				if connection != nil {
					mu.Lock()
					delete(active[c.Value], connection)
					mu.Unlock()
					if expiry != nil {
						expiry.Stop()
					}
				}
			}()
			nodes.route(tracked, r)
		} else {
			nodes.route(w, r)
		}
	})
	fs := http.FileServer(http.Dir(cfg.web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		fs.ServeHTTP(w, r)
	})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'")
		if access.origin != "" && !access.trusted(r) {
			fail(w, 403, fmt.Errorf("仅允许来自已配置反向代理的请求"))
			return
		}
		mux.ServeHTTP(w, r)
	})
	server := &http.Server{Addr: cfg.listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if panelCert != "" {
		log.Printf("OwnPanel listening on https://%s", cfg.listen)
		log.Fatal(server.ListenAndServeTLS(panelCert, panelKey))
	}
	log.Printf("OwnPanel listening on http://%s", cfg.listen)
	log.Fatal(server.ListenAndServe())
}
func validName(s string) bool {
	if len(s) == 0 || len(s) > 100 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.", c)) {
			return false
		}
	}
	return s != "." && s != ".."
}
