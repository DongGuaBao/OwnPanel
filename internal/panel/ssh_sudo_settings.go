package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (m *nodeManager) updateSudo(w http.ResponseWriter, r *http.Request, node Node) {
	if node.Mode != "ssh" {
		fail(w, 400, fmt.Errorf("只有 SSH 主机需要设置 sudo"))
		return
	}
	var body struct {
		Mode     string `json:"mode"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if body.Mode != "none" && body.Mode != "nopasswd" && body.Mode != "password" {
		fail(w, 400, fmt.Errorf("提权模式无效"))
		return
	}
	var originalData string
	var originalSecret []byte
	if err := m.db.QueryRow("SELECT data,secret FROM nodes WHERE id=?", node.ID).Scan(&originalData, &originalSecret); err != nil {
		fail(w, 409, fmt.Errorf("主机已经变化"))
		return
	}
	if err := json.Unmarshal([]byte(originalData), &node); err != nil {
		fail(w, 500, err)
		return
	}
	secret, err := m.decryptSSHSecret(node.ID, originalSecret)
	if err != nil {
		fail(w, 500, err)
		return
	}
	node.SudoMode = body.Mode
	node.Sudo = body.Mode != "none"
	if body.Password != "" {
		secret.SudoPassword = body.Password
	}
	if body.Mode != "password" {
		secret.SudoPassword = ""
	}
	if err = validateSSH(node, secret); err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	candidate := newSSHDriver(node, secret)
	defer candidate.Close()
	caps, err := candidate.Probe(ctx)
	if err != nil || node.Sudo && !caps["terminal.root"].Supported {
		fail(w, 400, fmt.Errorf("提权验证失败，原设置已保留；请检查密码和 sudo 权限"))
		return
	}
	node.Online = true
	node.LastError = ""
	node.LastSeen = time.Now().UTC().Format(time.RFC3339)
	node.Capabilities = caps
	encrypted, err := m.encrypt(node.ID, secret)
	if err != nil {
		fail(w, 500, err)
		return
	}
	data, _ := json.Marshal(node)
	m.mu.Lock()
	result, err := m.db.ExecContext(ctx, "UPDATE nodes SET data=?,secret=? WHERE id=? AND secret=?", string(data), encrypted, node.ID, originalSecret)
	var old nodeDriver
	count := int64(0)
	if err == nil {
		count, _ = result.RowsAffected()
	}
	if count == 1 {
		old = m.drivers[node.ID]
		delete(m.drivers, node.ID)
	}
	m.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	if count != 1 {
		fail(w, 409, fmt.Errorf("主机设置已经变化，请刷新后重试"))
		return
	}
	if old != nil {
		old.Close()
	}
	m.record(node.ID, "ssh_sudo_updated", map[string]string{"mode": body.Mode})
	respond(w, map[string]any{"node": node})
}
