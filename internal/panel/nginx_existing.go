package panel

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

//go:embed scripts/nginx_existing.py
var existingNginxProgram string

func serveExistingNginx(w http.ResponseWriter, r *http.Request, run systemdRunner, managedPrefix string, binary string) {
	request := map[string]any{}
	if r.Method == http.MethodGet {
		for k, values := range r.URL.Query() {
			if len(values) > 0 {
				request[k] = values[0]
			}
		}
	} else if r.Method == http.MethodPost {
		if e := decode(w, r, &request); e != nil {
			fail(w, 400, e)
			return
		}
	} else {
		w.WriteHeader(405)
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/api/nginx/existing") {
	case "":
		request["action"] = "inventory"
	case "/file":
		if r.Method == "GET" {
			request["action"] = "read"
		} else {
			request["action"] = "save"
		}
	case "/restore":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		request["action"] = "restore"
	case "/logs":
		request["action"] = "logs"
	case "/control":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		request["action"] = "control"
	default:
		w.WriteHeader(404)
		return
	}
	request["excluded"] = []string{managedPrefix + "/sites"}
	delete(request, "extra")
	if binary != "" {
		request["extra"] = []map[string]string{{"binary": binary, "config": managedPrefix + "/nginx.conf", "prefix": managedPrefix + "/"}}
	}
	data, _ := json.Marshal(request)
	script := "exec python3 - <<'OWNPANEL_EXISTING_NGINX'\nimport json,base64\nREQUEST=json.loads(base64.b64decode('" + base64.StdEncoding.EncodeToString(data) + "'))\n" + existingNginxProgram + "\nOWNPANEL_EXISTING_NGINX\n"
	output, e := run(r.Context(), true, script)
	if e != nil {
		fail(w, 400, e)
		return
	}
	var result map[string]any
	if e = json.Unmarshal([]byte(output), &result); e != nil {
		fail(w, 502, fmt.Errorf("Nginx 配置发现响应无效"))
		return
	}
	if value, ok := result["status"].(float64); ok && value >= 400 {
		fail(w, int(value), fmt.Errorf("%v", result["error"]))
		return
	}
	respond(w, result)
}
func existingNginxSSHRunner(d *sshDriver) systemdRunner {
	return func(ctx context.Context, root bool, script string, args ...string) (string, error) {
		return d.runOutput(ctx, root, script, 0, args...)
	}
}
