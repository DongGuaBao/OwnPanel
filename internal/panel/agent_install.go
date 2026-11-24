package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *nodeManager) installAgentSSH(w http.ResponseWriter, r *http.Request, source Node) {
	if r.Method != "POST" || source.Mode != "ssh" {
		fail(w, 400, fmt.Errorf("请选择 SSH 主机"))
		return
	}
	var body struct {
		Target string `json:"target"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	target, err := m.get(body.Target)
	if err != nil || target.Mode != "agent" {
		fail(w, 400, fmt.Errorf("请选择 Agent 注册主机"))
		return
	}
	driver, err := m.driver(source)
	if err != nil {
		fail(w, 500, err)
		return
	}
	d := driver.(*sshDriver)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	arch, err := d.run(ctx, true, "set -eu\ntest ! -f /var/lib/ownpanel-agent/identity.json\ncommand -v systemctl >/dev/null\ncommand -v python3 >/dev/null\nuname -m\n")
	if err != nil {
		fail(w, 400, err)
		return
	}
	architecture := map[string]string{"x86_64": "amd64", "aarch64": "arm64"}[strings.TrimSpace(arch)]
	if architecture == "" {
		fail(w, 400, fmt.Errorf("不支持的目标架构"))
		return
	}
	executable, _ := os.Executable()
	binaryPath := filepath.Join(filepath.Dir(executable), "agents", "linux-"+architecture, "ownpanel-agent")
	binary, err := os.Open(binaryPath)
	if err != nil {
		fail(w, 400, fmt.Errorf("中心缺少安装包 %s", binaryPath))
		return
	}
	defer binary.Close()
	enrollment, err := m.createEnrollment(target.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	client, err := d.sftp(ctx)
	if err != nil {
		fail(w, 502, err)
		return
	}
	defer client.Close()
	directory := "/var/tmp/ownpanel-install-" + token()[:24]
	if err = client.Mkdir(directory); err != nil {
		fail(w, 502, err)
		return
	}
	defer client.RemoveAll(directory)
	if err = client.Chmod(directory, 0700); err != nil {
		fail(w, 502, err)
		return
	}
	upload := func(name string, reader io.Reader) error {
		f, e := client.OpenFile(directory+"/"+name, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
		if e != nil {
			return e
		}
		defer f.Close()
		if e = f.Chmod(0600); e != nil {
			return e
		}
		_, e = io.Copy(f, reader)
		return e
	}
	if err = upload("agent", binary); err != nil {
		fail(w, 502, err)
		return
	}
	data, _ := json.Marshal(enrollment)
	if err = upload("enrollment.json", strings.NewReader(string(data))); err != nil {
		fail(w, 502, err)
		return
	}
	script := `set -eu
test ! -f /var/lib/ownpanel-agent/identity.json
if ! test -x /opt/ownpanel-nginx/sbin/nginx; then
 command -v apt-get >/dev/null || { echo '此系统需要先提供 /opt/ownpanel-nginx/sbin/nginx' >&2; exit 1; }
 export DEBIAN_FRONTEND=noninteractive
 apt-get update >/dev/null
 apt-get install -y build-essential libpcre2-dev libssl-dev zlib1g-dev curl >/dev/null
 build=$(mktemp -d /var/tmp/ownpanel-nginx-build.XXXXXXXX)
 trap 'rm -rf -- "$build"' EXIT
 curl --fail --silent --show-error --location --proto '=https' https://nginx.org/download/nginx-1.30.6.tar.gz -o "$build/nginx.tar.gz"
 tar -xzf "$build/nginx.tar.gz" -C "$build"
 (
 cd "$build/nginx-1.30.6"
 ./configure --prefix=/opt/ownpanel-nginx --with-http_ssl_module --with-http_v2_module --with-http_realip_module --with-stream --with-stream_ssl_module >/dev/null
 make -j2 >/dev/null
 install -d /opt/ownpanel-nginx/sbin /opt/ownpanel-nginx/conf
 install -m0755 objs/nginx /opt/ownpanel-nginx/sbin/nginx
 install -m0644 conf/mime.types /opt/ownpanel-nginx/conf/mime.types
 )
fi
install -d -m 0755 /opt/ownpanel-agent
install -d -m 0700 /var/lib/ownpanel-agent
install -m 0755 "$1/agent" /opt/ownpanel-agent/ownpanel-agent.new
mv /opt/ownpanel-agent/ownpanel-agent.new /opt/ownpanel-agent/ownpanel-agent
install -m 0600 "$1/enrollment.json" /var/lib/ownpanel-agent/enrollment.json
cat >/etc/systemd/system/ownpanel-agent.service <<'UNIT'
[Unit]
Description=OwnPanel Agent
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/opt/ownpanel-agent/ownpanel-agent -data /var/lib/ownpanel-agent -nginx-prefix /var/lib/ownpanel-agent-nginx -nginx-binary /opt/ownpanel-nginx/sbin/nginx -nginx-mime-types /opt/ownpanel-nginx/conf/mime.types -enrollment-file /var/lib/ownpanel-agent/enrollment.json
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now ownpanel-agent
`
	if _, err = d.run(ctx, true, script, directory); err != nil {
		fail(w, 400, err)
		return
	}
	respond(w, map[string]any{"ok": true, "message": "Agent 已安装并启动，请检查 Agent 主机连接状态"})
}
