package panel

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/yamux"
)

type agentPKI struct {
	ca     *x509.Certificate
	key    ed25519.PrivateKey
	caPEM  []byte
	server tls.Certificate
}
type agentSession struct{ session *yamux.Session }
type enrollment struct {
	NodeID  string    `json:"nodeId"`
	URL     string    `json:"url"`
	Token   string    `json:"token"`
	CA      string    `json:"ca"`
	Expires time.Time `json:"expires"`
}

func serial() *big.Int {
	n, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		panic(e)
	}
	return n
}
func privatePEM(key ed25519.PrivateKey) []byte {
	b, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		panic(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b})
}
func certPEM(b []byte) []byte { return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}) }
func newAgentPKI() (*agentPKI, error) {
	dir := filepath.Join(cfg.data, "pki")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	caPath, keyPath := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	caPEM, e := os.ReadFile(caPath)
	if os.IsNotExist(e) {
		_, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "OwnPanel Device CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
		der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
		if err != nil {
			return nil, err
		}
		caPEM = certPEM(der)
		if err = os.WriteFile(keyPath, privatePEM(key), 0600); err != nil {
			return nil, err
		}
		e = os.WriteFile(caPath, caPEM, 0600)
	}
	if e != nil {
		return nil, e
	}
	keyPEM, e := os.ReadFile(keyPath)
	if e != nil {
		return nil, e
	}
	pair, e := tls.X509KeyPair(caPEM, keyPEM)
	if e != nil {
		return nil, e
	}
	ca, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return nil, e
	}
	key, ok := pair.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("unexpected CA key")
	}
	_, serverKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return nil, e
	}
	t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "OwnPanel Center"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	for _, h := range strings.Split(cfg.agentHosts, ",") {
		h = strings.TrimSpace(h)
		if ip := net.ParseIP(h); ip != nil {
			t.IPAddresses = append(t.IPAddresses, ip)
		} else if h != "" {
			t.DNSNames = append(t.DNSNames, h)
		}
	}
	der, e := x509.CreateCertificate(rand.Reader, t, ca, serverKey.Public(), key)
	if e != nil {
		return nil, e
	}
	server, e := tls.X509KeyPair(certPEM(der), privatePEM(serverKey))
	return &agentPKI{ca: ca, key: key, caPEM: caPEM, server: server}, e
}
func (m *nodeManager) createEnrollment(id string) (enrollment, error) {
	t := token()
	v := enrollment{NodeID: id, URL: cfg.agentAdvertise, Token: t, CA: string(m.pki.caPEM), Expires: time.Now().Add(10 * time.Minute)}
	tx, e := m.db.Begin()
	if e != nil {
		return v, e
	}
	defer tx.Rollback()
	if _, e = tx.Exec("DELETE FROM enrollments WHERE node_id=? OR expires<?", id, time.Now().Unix()); e == nil {
		_, e = tx.Exec("INSERT INTO enrollments(hash,node_id,expires) VALUES(?,?,?)", digest([]byte(t)), id, v.Expires.Unix())
	}
	if e == nil {
		e = tx.Commit()
	}
	return v, e
}
func (m *nodeManager) listenAgents() {
	if cfg.agentListen == "" {
		return
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(m.pki.caPEM)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /renew", m.renewDeviceCertificate)
	mux.HandleFunc("POST /enroll", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Token string `json:"token"`
			CSR   string `json:"csr"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		block, _ := pem.Decode([]byte(b.CSR))
		if block == nil {
			fail(w, 400, fmt.Errorf("CSR 无效"))
			return
		}
		csr, e := x509.ParseCertificateRequest(block.Bytes)
		if e != nil || csr.CheckSignature() != nil {
			fail(w, 400, fmt.Errorf("CSR 签名无效"))
			return
		}
		if _, ok := csr.PublicKey.(ed25519.PublicKey); !ok {
			fail(w, 400, fmt.Errorf("设备密钥要求 Ed25519"))
			return
		}
		tx, e := m.db.BeginTx(r.Context(), nil)
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer tx.Rollback()
		var id string
		e = tx.QueryRow("SELECT node_id FROM enrollments WHERE hash=? AND expires>?", digest([]byte(b.Token)), time.Now().Unix()).Scan(&id)
		if e != nil {
			fail(w, 403, fmt.Errorf("注册码已使用或过期"))
			return
		}
		var exists int
		if e = tx.QueryRow("SELECT count(*) FROM nodes WHERE id=?", id).Scan(&exists); e != nil || exists != 1 {
			fail(w, 403, fmt.Errorf("设备已删除"))
			return
		}
		t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: id}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
		der, e := x509.CreateCertificate(rand.Reader, t, m.pki.ca, csr.PublicKey, m.pki.key)
		if e != nil {
			fail(w, 500, e)
			return
		}
		if _, e = tx.Exec("INSERT INTO agent_devices(node_id,serial) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET serial=excluded.serial", id, t.SerialNumber.String()); e != nil {
			fail(w, 500, e)
			return
		}
		if _, e = tx.Exec("DELETE FROM enrollments WHERE hash=?", digest([]byte(b.Token))); e == nil {
			e = tx.Commit()
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		m.record(id, "agent_enrolled", nil)
		m.closeNode(id)
		respond(w, map[string]string{"certificate": string(certPEM(der)), "nodeId": id})
	})
	mux.HandleFunc("GET /connect", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
			fail(w, 403, fmt.Errorf("设备认证失败"))
			return
		}
		if r.Header.Get("X-OwnPanel-Protocol") != "1" {
			fail(w, 426, fmt.Errorf("协议版本不兼容"))
			return
		}
		id := r.TLS.PeerCertificates[0].Subject.CommonName
		n, e := m.get(id)
		if e != nil || n.Mode != "agent" {
			fail(w, 403, fmt.Errorf("设备未注册或已撤销"))
			return
		}
		if !m.acceptDeviceCertificate(id, r.TLS.PeerCertificates[0].SerialNumber.String()) {
			fail(w, 403, fmt.Errorf("设备证书已撤销"))
			return
		}
		h, ok := w.(http.Hijacker)
		if !ok {
			fail(w, 500, fmt.Errorf("hijack unavailable"))
			return
		}
		c, rw, e := h.Hijack()
		if e != nil {
			return
		}
		if _, e = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: ownpanel\r\nConnection: Upgrade\r\n\r\n"); e == nil {
			e = rw.Flush()
		}
		if e != nil {
			c.Close()
			return
		}
		session, e := yamux.Client(&bufferedConn{Conn: c, reader: rw.Reader}, yamux.DefaultConfig())
		if e != nil {
			c.Close()
			return
		}
		a := &agentSession{session}
		m.mu.Lock()
		old := m.agents[id]
		m.agents[id] = a
		m.mu.Unlock()
		if old != nil {
			old.session.Close()
		}
		m.record(id, "agent_connected", nil)
		<-session.CloseChan()
		m.mu.Lock()
		if m.agents[id] == a {
			delete(m.agents, id)
		}
		m.mu.Unlock()
		m.record(id, "agent_disconnected", nil)
	})
	server := &http.Server{Addr: cfg.agentListen, Handler: mux, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{m.pki.server}, ClientCAs: pool, ClientAuth: tls.VerifyClientCertIfGiven, NextProtos: []string{"http/1.1"}}, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 45 * time.Second, MaxHeaderBytes: 16 << 10}
	log.Printf("Agent TLS listener: %s", cfg.agentListen)
	if e := server.ListenAndServeTLS("", ""); e != nil {
		log.Fatal(e)
	}
}

type agentIdentity struct {
	URL    string `json:"url"`
	NodeID string `json:"nodeId"`
	CA     string `json:"ca"`
}

func loadAgentIdentity() (agentIdentity, tls.Certificate, error) {
	var id agentIdentity
	identityPath := filepath.Join(cfg.data, "identity.json")
	b, e := os.ReadFile(identityPath)
	if e == nil {
		if e = json.Unmarshal(b, &id); e != nil {
			return id, tls.Certificate{}, e
		}
		pair, e := tls.LoadX509KeyPair(filepath.Join(cfg.data, "device.pem"), filepath.Join(cfg.data, "device.key"))
		return id, pair, e
	}
	if !os.IsNotExist(e) {
		return id, tls.Certificate{}, e
	}
	if cfg.enrollmentFile == "" {
		return id, tls.Certificate{}, fmt.Errorf("首次接入需要 -enrollment-file")
	}
	b, e = os.ReadFile(cfg.enrollmentFile)
	if e != nil {
		return id, tls.Certificate{}, e
	}
	var v enrollment
	if e = json.Unmarshal(b, &v); e != nil {
		return id, tls.Certificate{}, e
	}
	if !time.Now().Before(v.Expires) {
		return id, tls.Certificate{}, fmt.Errorf("注册码已过期")
	}
	u, e := url.Parse(v.URL)
	if e != nil || u.Scheme != "https" || u.Host == "" {
		return id, tls.Certificate{}, fmt.Errorf("中心 URL 必须为 HTTPS")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(v.CA)) {
		return id, tls.Certificate{}, fmt.Errorf("CA 无效")
	}
	_, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return id, tls.Certificate{}, e
	}
	der, e := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: v.NodeID}}, key)
	if e != nil {
		return id, tls.Certificate{}, e
	}
	body, _ := json.Marshal(map[string]string{"token": v.Token, "csr": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))})
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Post(strings.TrimRight(v.URL, "/")+"/enroll", "application/json", bytes.NewReader(body))
	if e != nil {
		return id, tls.Certificate{}, e
	}
	defer res.Body.Close()
	var response struct {
		Certificate string `json:"certificate"`
		NodeID      string `json:"nodeId"`
		Error       string `json:"error"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&response); e != nil {
		return id, tls.Certificate{}, e
	}
	if res.StatusCode != 200 {
		return id, tls.Certificate{}, fmt.Errorf("注册失败: %s", response.Error)
	}
	if response.NodeID != v.NodeID {
		return id, tls.Certificate{}, fmt.Errorf("设备身份不匹配")
	}
	keyPEM := privatePEM(key)
	pair, e := tls.X509KeyPair([]byte(response.Certificate), keyPEM)
	if e != nil {
		return id, pair, e
	}
	id = agentIdentity{URL: v.URL, NodeID: v.NodeID, CA: v.CA}
	for name, data := range map[string][]byte{"device.pem": []byte(response.Certificate), "device.key": keyPEM} {
		if e = os.WriteFile(filepath.Join(cfg.data, name), data, 0600); e != nil {
			return id, pair, e
		}
	}
	data, _ := json.Marshal(id)
	e = os.WriteFile(identityPath, data, 0600)
	if e == nil {
		_ = os.Remove(cfg.enrollmentFile)
	}
	return id, pair, e
}
func runOutboundAgent(handler http.Handler) {
	id, pair, e := loadAgentIdentity()
	if e != nil {
		log.Fatal(e)
	}
	if cfg.agentURL != "" && cfg.agentURL != id.URL {
		log.Fatal("center URL differs from enrolled identity")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(id.CA)) {
		log.Fatal("invalid CA")
	}
	u, e := url.Parse(id.URL)
	if e != nil {
		log.Fatal(e)
	}
	delay := time.Second
	for {
		started := time.Now()
		pair = renewAgentIdentity(u, pool, pair)
		e = serveAgentConnection(u, pool, pair, handler)
		log.Printf("Agent connection ended: %v; retrying", e)
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		}
		var jitter [1]byte
		_, _ = rand.Read(jitter[:])
		time.Sleep(delay + time.Duration(jitter[0])*time.Millisecond)
		if delay < 30*time.Second {
			delay *= 2
		}
	}
}
func serveAgentConnection(u *url.URL, pool *x509.CertPool, pair tls.Certificate, handler http.Handler) error {
	address := u.Host
	if u.Port() == "" {
		address = net.JoinHostPort(u.Hostname(), "443")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	raw, e := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if e != nil {
		return e
	}
	c := tls.Client(raw, &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, ServerName: u.Hostname(), MinVersion: tls.VersionTLS13})
	defer c.Close()
	refresh := time.AfterFunc(24*time.Hour, func() { _ = c.Close() })
	defer refresh.Stop()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if e = c.HandshakeContext(ctx); e != nil {
		return e
	}
	req, _ := http.NewRequest("GET", idURL(u, "/connect"), nil)
	req.Header.Set("X-OwnPanel-Protocol", "1")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "ownpanel")
	if e = req.Write(c); e != nil {
		return e
	}
	reader := bufio.NewReader(c)
	res, e := http.ReadResponse(reader, req)
	if e != nil {
		return e
	}
	if res.StatusCode != 101 {
		res.Body.Close()
		return fmt.Errorf("center response: %s", res.Status)
	}
	_ = c.SetDeadline(time.Time{})
	session, e := yamux.Server(&bufferedConn{Conn: c, reader: reader}, yamux.DefaultConfig())
	if e != nil {
		return e
	}
	defer session.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	return server.Serve(&streamListener{session: session, addr: c.LocalAddr()})
}
func idURL(u *url.URL, path string) string { return u.Scheme + "://" + u.Host + path }

type streamListener struct {
	session *yamux.Session
	addr    net.Addr
}

func (l *streamListener) Accept() (net.Conn, error) { return l.session.Accept() }
func (l *streamListener) Close() error              { return l.session.Close() }
func (l *streamListener) Addr() net.Addr            { return l.addr }
