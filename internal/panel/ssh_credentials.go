package panel

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func (m *nodeManager) rotateSSHCredentials(w http.ResponseWriter, r *http.Request, node Node) {
	if node.Mode != "ssh" {
		fail(w, http.StatusBadRequest, fmt.Errorf("该主机不使用 SSH 凭证"))
		return
	}
	var replacement SSHSecret
	if err := decode(w, r, &replacement); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	var original []byte
	if err := m.db.QueryRowContext(ctx, "SELECT secret FROM nodes WHERE id=?", node.ID).Scan(&original); err != nil {
		fail(w, 409, fmt.Errorf("主机已变更，请刷新后重试"))
		return
	}
	originalSecret, err := m.decryptSSHSecret(node.ID, original)
	if err != nil {
		fail(w, 500, err)
		return
	}
	if replacement.SudoPassword == "" {
		replacement.SudoPassword = originalSecret.SudoPassword
	}
	if err := validateSSH(node, replacement); err != nil {
		fail(w, 400, err)
		return
	}
	candidate := newSSHDriver(node, replacement)
	defer candidate.Close()
	if caps, err := candidate.Probe(ctx); err != nil || node.SudoMode == "password" && !caps["terminal.root"].Supported {
		fail(w, http.StatusBadRequest, fmt.Errorf("SSH 或 sudo 验证失败，未替换原凭证"))
		return
	}
	encrypted, err := m.encrypt(node.ID, replacement)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	result, err := m.db.ExecContext(ctx, "UPDATE nodes SET secret=? WHERE id=? AND secret=?", encrypted, node.ID, original)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if count, _ := result.RowsAffected(); count != 1 {
		fail(w, http.StatusConflict, fmt.Errorf("凭证已变更，请刷新后重试"))
		return
	}
	m.closeNode(node.ID)
	m.record(node.ID, "ssh_credentials_rotated", nil)
	respond(w, map[string]bool{"ok": true})
}
