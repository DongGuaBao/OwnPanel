package panel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func iptablesRequest(w http.ResponseWriter, r *http.Request) (map[string]any, error) {
	if r.Method == http.MethodGet {
		program := r.URL.Query().Get("program")
		if program == "" {
			program = "iptables"
		}
		if !validIPTablesProgram(program) {
			return nil, fmt.Errorf("iptables 后端无效")
		}
		return map[string]any{"action": "iptables-status", "program": program}, nil
	}
	if r.Method != http.MethodPost {
		return nil, fmt.Errorf("请求方式无效")
	}
	if strings.HasSuffix(r.URL.Path, "/confirm") {
		var body struct {
			ID string `json:"id"`
		}
		if err := decode(w, r, &body); err != nil {
			return nil, err
		}
		return map[string]any{"action": "confirm", "id": body.ID}, nil
	}
	var body struct {
		Program string `json:"program"`
		Rules   string `json:"rules"`
	}
	if err := decode(w, r, &body); err != nil {
		return nil, err
	}
	if !validIPTablesProgram(body.Program) || strings.TrimSpace(body.Rules) == "" {
		return nil, fmt.Errorf("请选择后端并输入 iptables-save 格式规则")
	}
	return map[string]any{"action": "apply", "backend": "iptables", "program": body.Program, "rules": body.Rules, "id": token()[:16], "recoveryProgram": sshFirewallProgram}, nil
}
func validIPTablesProgram(p string) bool {
	return p == "iptables" || p == "ip6tables" || p == "iptables-legacy" || p == "ip6tables-legacy" || p == "iptables-nft" || p == "ip6tables-nft"
}
func firewallPython(request map[string]any) string {
	data, _ := json.Marshal(request)
	return "import base64,json\nREQUEST=json.loads(base64.b64decode('" + base64.StdEncoding.EncodeToString(data) + "'))\n" + sshFirewallProgram
}
func firewallResult(w http.ResponseWriter, out string) {
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		fail(w, 502, fmt.Errorf("防火墙响应无效"))
		return
	}
	if code, ok := result["status"].(float64); ok && code == 409 {
		fail(w, 409, fmt.Errorf("%v", result["error"]))
		return
	}
	respond(w, result)
}
func (d *sshDriver) iptables(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	caps, err := d.Probe(ctx)
	if err != nil {
		fail(w, 502, err)
		return
	}
	capability := "iptables.read"
	if r.Method != http.MethodGet {
		capability = "iptables.write"
	}
	if !caps[capability].Supported {
		fail(w, 403, fmt.Errorf("%s", caps[capability].Reason))
		return
	}
	request, err := iptablesRequest(w, r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	worker := d
	if request["action"] == "confirm" {
		worker = newSSHDriver(d.n, d.secret)
		defer worker.Close()
	}
	script := "exec python3 - <<'OWNPANEL_IPTABLES'\n" + firewallPython(request) + "\nOWNPANEL_IPTABLES\n"
	out, err := worker.runOutput(ctx, true, script, 0)
	if err != nil {
		fail(w, 400, err)
		return
	}
	firewallResult(w, out)
}
