package panel

import (
	"bytes"
	"context"

	"fmt"
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type sshDriver struct {
	sudoAuthBlocked bool
	loginRoot       bool
	n               Node
	secret          SSHSecret
	mu              sync.Mutex
	control, bulk   *ssh.Client
	closed          bool
	nextTry         time.Time
	failures        int
	lastErr         error
	caps            map[string]Capability
	lastProbe       time.Time
	root            bool
	nginxMu         sync.Mutex
	metricsMu       sync.Mutex
	previousCPU     []float64
	terminals       *terminalHub
	previousBoot    string
}

func validateAddress(host string, port int) error {
	if host == "" || strings.ContainsAny(host, "/\x00\r\n\t ") || port < 1 || port > 65535 {
		return fmt.Errorf("SSH 地址或端口无效")
	}
	return nil
}
func validateSSH(n Node, s SSHSecret) error {
	if n.SudoMode != "" && n.SudoMode != "none" && n.SudoMode != "nopasswd" && n.SudoMode != "password" {
		return fmt.Errorf("sudo 模式无效")
	}
	if n.SudoMode == "password" && n.User != "root" {
		password := s.SudoPassword
		if password == "" {
			password = s.Password
		}
		if password == "" || strings.ContainsAny(password, "\r\n\x00") {
			return fmt.Errorf("请输入有效的 sudo 密码；留空时使用 SSH 登录密码")
		}
	}

	if e := validateAddress(n.Host, n.Port); e != nil {
		return e
	}
	if n.User == "" || strings.ContainsAny(n.User, "\x00\r\n") {
		return fmt.Errorf("SSH 用户名无效")
	}
	if !strings.HasPrefix(n.Fingerprint, "SHA256:") || len(n.Fingerprint) < 40 {
		return fmt.Errorf("请先获取并核实 SSH 主机指纹")
	}
	if s.Password == "" && s.PrivateKey == "" {
		return fmt.Errorf("请输入密码或 SSH 私钥")
	}
	if s.Password != "" && s.PrivateKey != "" {
		return fmt.Errorf("请选择一种认证方式")
	}
	if s.PrivateKey != "" {
		_, e := sshSigner(s)
		if e != nil {
			return fmt.Errorf("私钥无法解析: %w", e)
		}
	}
	return nil
}
func sshSigner(s SSHSecret) (ssh.Signer, error) {
	if s.Passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(s.PrivateKey), []byte(s.Passphrase))
	}
	return ssh.ParsePrivateKey([]byte(s.PrivateKey))
}
func probeSSHHost(ctx context.Context, host string, port int) (string, error) {
	if e := validateAddress(host, port); e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	address := net.JoinHostPort(host, strconv.Itoa(port))
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if e != nil {
		return "", e
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	fingerprint := ""
	config := &ssh.ClientConfig{User: "probe", HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		fingerprint = ssh.FingerprintSHA256(key)
		return fmt.Errorf("fingerprint probe completed before authentication")
	}}
	_, _, _, e = ssh.NewClientConn(c, address, config)
	if fingerprint != "" {
		return fingerprint, nil
	}
	return "", e
}
func newSSHDriver(n Node, s SSHSecret) *sshDriver {
	d := &sshDriver{n: n, secret: s}
	d.terminals = &terminalHub{start: d.startTerminal}
	return d
}
func (d *sshDriver) connect(ctx context.Context, bulk bool) (*ssh.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil, fmt.Errorf("主机连接已关闭")
	}
	current := d.control
	if bulk {
		current = d.bulk
	}
	if current != nil {
		return current, nil
	}
	if time.Now().Before(d.nextTry) {
		return nil, fmt.Errorf("SSH 正在退避重连: %v", d.lastErr)
	}
	auth := []ssh.AuthMethod{}
	if d.secret.PrivateKey != "" {
		s, e := sshSigner(d.secret)
		if e != nil {
			return nil, e
		}
		auth = append(auth, ssh.PublicKeys(s))
	} else {
		auth = append(auth, ssh.Password(d.secret.Password))
	}
	config := &ssh.ClientConfig{User: d.n.User, Auth: auth, Timeout: 10 * time.Second, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if ssh.FingerprintSHA256(key) != d.n.Fingerprint {
			return fmt.Errorf("SSH 主机指纹变化，已阻止连接；请核实目标身份")
		}
		return nil
	}}
	address := net.JoinHostPort(d.n.Host, strconv.Itoa(d.n.Port))
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, e := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if e == nil {
		deadline := time.Now().Add(10 * time.Second)
		if v, ok := dialCtx.Deadline(); ok && v.Before(deadline) {
			deadline = v
		}
		_ = raw.SetDeadline(deadline)
		var conn ssh.Conn
		var channels <-chan ssh.NewChannel
		var requests <-chan *ssh.Request
		conn, channels, requests, e = ssh.NewClientConn(raw, address, config)
		if e == nil {
			_ = raw.SetDeadline(time.Time{})
			client := ssh.NewClient(conn, channels, requests)
			if bulk {
				d.bulk = client
			} else {
				d.control = client
			}
			d.failures = 0
			d.lastErr = nil
			go d.keepalive(client, bulk)
			return client, nil
		}
		raw.Close()
	}
	d.failures++
	backoff := time.Duration(1<<min(d.failures, 5)) * time.Second
	d.nextTry = time.Now().Add(backoff)
	d.lastErr = e
	return nil, e
}
func (d *sshDriver) invalidate(client *ssh.Client) {
	d.mu.Lock()
	if d.control == client {
		d.control = nil
	}
	if d.bulk == client {
		d.bulk = nil
	}
	d.mu.Unlock()
	client.Close()
}
func (d *sshDriver) keepalive(client *ssh.Client, bulk bool) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	closed := clientDone(client)
	for {
		select {
		case <-ticker.C:
			done := make(chan error, 1)
			go func() { _, _, e := client.SendRequest("keepalive@openssh.com", true, nil); done <- e }()
			select {
			case e := <-done:
				if e != nil {
					d.invalidate(client)
					return
				}
			case <-time.After(8 * time.Second):
				d.invalidate(client)
				return
			}
		case <-closed:
			d.invalidate(client)
			return
		}
	}
}

// ssh.Client has no public Done channel; Wait is run once per keepalive loop.
func clientDone(c *ssh.Client) <-chan struct{} {
	ch := make(chan struct{})
	go func() { _ = c.Wait(); close(ch) }()
	return ch
}
func (d *sshDriver) Close() {
	d.terminals.close()
	d.mu.Lock()
	d.closed = true
	c, b := d.control, d.bulk
	d.control = nil
	d.bulk = nil
	d.mu.Unlock()
	if c != nil {
		c.Close()
	}
	if b != nil {
		b.Close()
	}
}

type boundedOutput struct {
	buffer bytes.Buffer
	mu     sync.Mutex
	limit  int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit > 0 && b.buffer.Len()+len(p) > b.limit {
		return 0, fmt.Errorf("远端输出超过限制")
	}
	return b.buffer.Write(p)
}
func (b *boundedOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
func (d *sshDriver) run(ctx context.Context, privileged bool, script string, args ...string) (string, error) {
	return d.runOutput(ctx, privileged, script, 4<<20, args...)
}
func (d *sshDriver) runOutput(ctx context.Context, privileged bool, script string, limit int, args ...string) (string, error) {
	client, e := d.connect(ctx, false)
	if e != nil {
		return "", e
	}
	type sessionResult struct {
		s *ssh.Session
		e error
	}
	created := make(chan sessionResult)
	go func() {
		s, e := client.NewSession()
		select {
		case created <- sessionResult{s, e}:
		case <-ctx.Done():
			if s != nil {
				s.Close()
			}
		}
	}()
	var s *ssh.Session
	select {
	case v := <-created:
		s, e = v.s, v.e
	case <-ctx.Done():
		d.invalidate(client)
		return "", ctx.Err()
	}
	if e != nil {
		d.invalidate(client)
		return "", e
	}
	defer s.Close()
	command := "LC_ALL=C /bin/sh -s --"
	if privileged && d.n.User != "root" {
		if !d.n.Sudo {
			return "", fmt.Errorf("此操作需要 root 或已配置的免交互 sudo")
		}
		command = "sudo -n -- /bin/sh -s --"
	}
	for _, arg := range args {
		command += " " + shQuote(arg)
	}

	output := &boundedOutput{limit: limit}
	stderr := &boundedOutput{limit: 1 << 20}
	s.Stdout = output
	s.Stderr = stderr
	var done <-chan error
	if privileged && d.passwordSudo() {
		target := "/bin/sh -s --"
		for _, arg := range args {
			target += " " + shQuote(arg)
		}
		input, finished, err := d.startPasswordSudo(ctx, s, target, false)
		if err != nil {
			return "", err
		}
		done = finished
		go func() { _, _ = io.WriteString(input, script); _ = input.Close() }()
	} else {
		s.Stdin = strings.NewReader(script)
		finished := make(chan error, 1)
		done = finished
		go func() { finished <- s.Run(command) }()
	}
	select {
	case e = <-done:
		if e != nil {
			return output.String(), fmt.Errorf("SSH 执行失败: %w: %s %s", e, output.String(), stderr.String())
		}
		return output.String(), nil
	case <-ctx.Done():
		_ = s.Signal(ssh.SIGTERM)
		s.Close()
		return "", fmt.Errorf("操作超时或连接已取消；变更结果需核实: %w", ctx.Err())
	}
}
func (d *sshDriver) Probe(ctx context.Context) (map[string]Capability, error) {
	script := `set -eu
printf 'uid='; id -u
for c in nginx ufw systemctl journalctl zip unzip python3 tar iptables iptables-save iptables-restore; do if command -v "$c" >/dev/null 2>&1; then printf '%s=1\n' "$c"; fi; done
if test -x /www/server/nginx/sbin/nginx || test -x /usr/local/nginx/sbin/nginx; then echo nginx=1; fi
if test -x /usr/bin/python3 && test -d /run/systemd/system && systemctl show --property=Version --value >/dev/null 2>&1; then echo firewall_recovery=1; fi
if test -r /proc/stat && test -r /proc/meminfo; then echo metrics=1; fi
if test -r "$1"; then echo file_read=1; fi
if test -w "$1"; then echo file_write=1; fi
if test "$(id -u)" = 0; then echo root=1; elif test "$2" = 1 && sudo -n -- /bin/sh -c 'test "$(id -u)" = 0' >/dev/null 2>&1; then echo root=1; fi
`
	out, e := d.run(ctx, false, script, d.n.FileRoot, map[bool]string{true: "1", false: "0"}[d.n.Sudo])
	if e != nil {
		return nil, e
	}
	loginRoot := strings.Contains("\n"+out, "\nuid=0\n")
	flags := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		p := strings.SplitN(line, "=", 2)
		if len(p) == 2 {
			flags[p[0]] = p[1] == "1"
		}
	}
	root := flags["root"]
	if d.passwordSudo() && !loginRoot {
		proof, proofErr := d.run(ctx, true, "id -u\n")
		root = proofErr == nil && strings.TrimSpace(proof) == "0"
	}

	caps := map[string]Capability{}
	add := func(k string, yes bool, reason string) {
		caps[k] = Capability{Supported: yes}
		if !yes {
			caps[k] = Capability{Reason: reason}
		}
	}
	add("metrics", flags["metrics"], "目标不提供 Linux /proc 指标")
	add("files.read", root, "全盘文件管理需要 root 或有效 sudo")
	add("files.write", root, "全盘文件管理需要 root 或有效 sudo")
	add("terminal", true, "")
	add("systemd.read", flags["systemctl"], "需要 systemd")
	add("systemd.write", flags["systemctl"] && root, "需要 systemd 和 root/有效 sudo")
	add("terminal.root", root, "当前账号没有 root 或有效 sudo；请检查提权模式、密码与权限")
	add("nginx.read", flags["nginx"] && flags["python3"], "需要目标安装 Nginx 和 Python 3")
	add("nginx.write", flags["nginx"] && flags["python3"] && root, "需要 Nginx、Python 3 和 root/有效 sudo")
	add("logs.ssh", root, "SSH 认证日志需要特权读取")
	add("logs.audit", false, "SSH 模式操作记录保存在中心审计中")
	add("firewall.read", flags["ufw"] && root, "需要 UFW 和特权读取")
	add("firewall.write", flags["ufw"] && flags["python3"] && flags["tar"] && flags["firewall_recovery"] && root, "需要 UFW、Python 3、tar、systemd 和 root/有效 sudo")
	add("iptables.read", flags["iptables-save"] && flags["python3"] && root, "需要 iptables、Python 3 与 root/有效 sudo")
	add("iptables.write", flags["iptables-save"] && flags["iptables-restore"] && flags["python3"] && flags["firewall_recovery"] && root, "需要 iptables-restore、Python 3、systemd 与 root/有效 sudo")
	d.mu.Lock()
	d.caps = caps
	d.root = root
	d.loginRoot = loginRoot
	d.lastProbe = time.Now()
	d.mu.Unlock()
	return caps, nil
}
func (d *sshDriver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	p := strings.TrimPrefix(r.URL.Path, "/api/")
	if p == "terminal" || p == "terminals" || strings.HasPrefix(p, "terminals/") {
		d.terminals.ServeHTTP(w, r)
		return
	}
	if p == "status" {
		caps, e := d.Probe(ctx)
		if e != nil {
			fail(w, 502, e)
			return
		}
		d.mu.Lock()
		root := d.root
		d.mu.Unlock()
		respond(w, map[string]any{"version": "0.6.0", "mode": "ssh", "fileRoot": "/", "capabilities": caps, "root": root, "nginxPrefix": "/etc/nginx/ownpanel"})
		return
	}
	switch {
	case strings.HasPrefix(p, "docker/"):
		serveDocker(w, r, []dockerEngine{{"login", d.n.User + " · SSH"}}, func(string) dockerRunner {
			return func(ctx context.Context, args ...string) (string, error) {
				script := `exec docker "$@"`
				if len(args) > 1 && args[1] == "logs" {
					script += ` 2>&1`
				}
				return d.run(ctx, false, script, args...)
			}
		})
	case p == "metrics":
		v, e := d.metrics(ctx)
		if e != nil {
			fail(w, 502, e)
		} else {
			respond(w, v)
		}
	case strings.HasPrefix(p, "files"):
		d.files(w, r, r.Context())
	case strings.HasPrefix(p, "nginx/existing"):
		serveExistingNginx(w, r.WithContext(ctx), existingNginxSSHRunner(d), "/etc/nginx/ownpanel", "")
	case strings.HasPrefix(p, "nginx"):
		d.nginx(w, r, ctx)
	case p == "logs/ssh":
		out, e := d.run(ctx, true, `if command -v journalctl >/dev/null 2>&1; then journalctl -u ssh.service -u sshd.service -n 300 --no-pager -o short-iso; elif test -r /var/log/auth.log; then tail -n 300 /var/log/auth.log; else tail -n 300 /var/log/secure; fi`)
		if e != nil {
			fail(w, 403, e)
		} else {
			respond(w, map[string]string{"text": out})
		}
	case p == "iptables" || p == "iptables/confirm":
		d.iptables(w, r, ctx)
	case p == "systemd" || strings.HasPrefix(p, "systemd/"):
		serveHostSystemd(w, r.WithContext(ctx), d.run, d.n.User == "root" || d.n.Sudo, d.n.User)
	case p == "firewall" || p == "firewall/confirm":
		d.firewall(w, r, ctx)
	default:
		fail(w, 501, fmt.Errorf("此连接模式暂不支持该操作"))
	}
}
func (d *sshDriver) remotePath(p string) (string, error) {
	rel, e := relativeRemote(p)
	if e != nil {
		return "", e
	}
	return path.Join(d.n.FileRoot, rel), nil
}
func relativeRemote(p string) (string, error) {
	if strings.ContainsAny(p, "\x00\\\r\n") {
		return "", fmt.Errorf("路径无效")
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ".", nil
	}
	for _, v := range strings.Split(p, "/") {
		if v == ".." || v == "" {
			return "", fmt.Errorf("路径无效")
		}
	}
	return p, nil
}
