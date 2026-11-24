package panel

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

//go:embed scripts/ssh_firewall.py
var sshFirewallProgram string

func (d *sshDriver) firewall(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	capabilities, err := d.Probe(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if !capabilities["firewall.write"].Supported {
		if r.Method != http.MethodGet || r.URL.Path != "/api/firewall" {
			fail(w, http.StatusForbidden, fmt.Errorf("%s", capabilities["firewall.write"].Reason))
			return
		}
		out, err := d.run(ctx, true, "ufw status numbered\n")
		if err != nil {
			fail(w, http.StatusForbidden, err)
			return
		}
		added, err := d.run(ctx, true, "ufw show added\n")
		if err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
		respond(w, map[string]any{"text": out, "added": added, "pending": nil, "backend": "ufw", "readOnly": true})
		return
	}
	request := map[string]any{"action": "status"}
	worker := d
	switch {
	case r.URL.Path == "/api/firewall" && r.Method == http.MethodGet:
	case r.URL.Path == "/api/firewall" && r.Method == http.MethodPost:
		var change firewallChange
		if err := decode(w, r, &change); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		added, err := d.run(ctx, true, "ufw show added\n")
		if err != nil {
			fail(w, 502, err)
			return
		}
		commands, err := firewallCommands(change, added)
		if err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		request = map[string]any{
			"action": "apply", "operation": change.Action, "arguments": commands[0], "commands": commands, "original": change.Original,
			"id": token()[:16], "recoveryProgram": sshFirewallProgram,
		}
		if change.Action == "enable" {
			connection, err := d.run(ctx, false, `printf '%s' "$SSH_CONNECTION"`)
			fields := strings.Fields(connection)
			if err != nil || len(fields) != 4 {
				fail(w, http.StatusConflict, fmt.Errorf("无法识别目标 SSH 监听端口"))
				return
			}
			port, err := strconv.Atoi(fields[3])
			if err != nil || port < 1 || port > 65535 {
				fail(w, http.StatusConflict, fmt.Errorf("目标 SSH 监听端口无效"))
				return
			}
			request["sshPort"] = port
		}
	case r.URL.Path == "/api/firewall/confirm" && r.Method == http.MethodPost:
		var body struct {
			ID string `json:"id"`
		}
		if err := decode(w, r, &body); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
		request = map[string]any{"action": "confirm", "id": body.ID}
		// Confirmation verifies a fresh TCP connection through the changed rules.
		worker = newSSHDriver(d.n, d.secret)
		defer worker.Close()
	default:
		fail(w, http.StatusMethodNotAllowed, fmt.Errorf("防火墙请求方式无效"))
		return
	}
	data, err := json.Marshal(request)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	script := "exec python3 - <<'OWNPANEL_FIREWALL'\nimport base64,json\nREQUEST=json.loads(base64.b64decode('" + base64.StdEncoding.EncodeToString(data) + "'))\n" + sshFirewallProgram + "\nOWNPANEL_FIREWALL\n"
	out, err := worker.run(ctx, true, script)
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		fail(w, http.StatusBadGateway, fmt.Errorf("远端防火墙响应无效"))
		return
	}
	if code, ok := result["status"].(float64); ok && code == http.StatusConflict {
		fail(w, http.StatusConflict, fmt.Errorf("%v", result["error"]))
		return
	}
	respond(w, result)
}
