package panel

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNginxCustomTLSAndQUICConfiguration(t *testing.T) {
	binary, err := filepath.Abs("../../tools/nginx/sbin/nginx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(binary); err != nil {
		t.Skip("private Nginx not built")
	}
	version, err := exec.Command(binary, "-V").CombinedOutput()
	if err != nil {
		t.Fatal(string(version), err)
	}
	if !strings.Contains(string(version), "--with-http_v3_module") {
		t.Skip("Nginx HTTP/3 module unavailable")
	}
	chain, key := testCertificatePair(t, time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	freePort := func() int {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		return port
	}
	tlsPort, quicPort := freePort(), freePort()
	prefix := t.TempDir()
	if err = os.MkdirAll(filepath.Join(prefix, "logs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeSite := func(s site) {
		t.Helper()
		dir := filepath.Join(prefix, "sites", s.ID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, data := range renderSiteAt(s, prefix) {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := site{ID: "protocols", Domains: "example.test", Kind: "locations", Routing: "return 200 'protocol-ok';", TLS: true, HTTPEnabled: protocolFlag(false), HTTPSEnabled: protocolFlag(true), TLSPort: tlsPort, QUIC: true, QUICPort: quicPort, Certificate: chain, PrivateKey: key}
	writeSite(s)
	main := filepath.Join(prefix, "nginx.conf")
	config := "worker_processes 1;\npid " + q(filepath.Join(prefix, "nginx.pid")) + ";\nerror_log " + q(filepath.Join(prefix, "logs/error.log")) + ";\nevents {}\nhttp { access_log off; include " + q(filepath.Join(prefix, "sites/protocols/server.conf")) + "; }\n"
	if err = os.WriteFile(main, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		out, err := exec.Command(binary, "-t", "-p", prefix+"/", "-c", main).CombinedOutput()
		if err != nil {
			t.Fatalf("Nginx rejected generated config: %v %s", err, out)
		}
	}
	check()
	// QUIC-only must be accepted without any TCP HTTP or HTTPS listener.
	s.HTTPSEnabled = protocolFlag(false)
	writeSite(s)
	check()
	// SNI bindings sharing the same UDP port must not repeat reuseport options.
	s.Bindings = []certificateBinding{{ID: "one", Domains: "example.test", Certificate: chain, PrivateKey: key}, {ID: "two", Domains: "other.example.test", Certificate: chain, PrivateKey: key}}
	writeSite(s)
	check()
	s.Bindings = nil
	s.HTTPSEnabled = protocolFlag(true)
	writeSite(s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-p", prefix+"/", "-c", main, "-g", "daemon off;")
	log, err := os.Create(filepath.Join(prefix, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGQUIT)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cancel()
			<-done
		}
	}()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(chain))
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "example.test"}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	var response *http.Response
	for i := 0; i < 50; i++ {
		response, err = client.Get("https://127.0.0.1:" + strings.TrimPrefix(net.JoinHostPort("", strconv.Itoa(tlsPort)), ":") + "/")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("custom TLS listener not ready", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if string(body) != "protocol-ok" || !strings.Contains(response.Header.Get("Alt-Svc"), strconv.Itoa(quicPort)) {
		t.Fatal(string(body), response.Header)
	}
}
