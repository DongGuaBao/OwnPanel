package panel

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func systemdRead(ctx context.Context, run systemdRunner, script string) (string, error) {
	out, e := run(ctx, true, script)
	if e != nil {
		return run(ctx, false, script)
	}
	return out, e
}
func systemdLogUnitFlag(ctx context.Context) string {
	if systemdUserScope(ctx) {
		return "--user-unit"
	}
	return "-u"
}

func serveSystemdLogs(w http.ResponseWriter, r *http.Request, run systemdRunner) {
	name := r.URL.Query().Get("unit")
	if !systemdUnitName.MatchString(name) || len(name) > 255 {
		fail(w, 400, fmt.Errorf("单元名称无效"))
		return
	}
	lines := 500
	if value := r.URL.Query().Get("lines"); value != "" {
		n, e := strconv.Atoi(value)
		if e != nil || n < 20 || n > 2000 {
			fail(w, 400, fmt.Errorf("日志行数需为 20 至 2000"))
			return
		}
		lines = n
	}
	args := []string{"--no-pager", "--no-hostname", "-o", "short-iso", "-n", strconv.Itoa(lines), systemdLogUnitFlag(r.Context()), name}
	ranges := map[string]string{"5m": "5 minutes ago", "1h": "1 hour ago", "24h": "24 hours ago"}
	since := r.URL.Query().Get("since")
	if since != "" && since != "all" {
		v, ok := ranges[since]
		if !ok {
			fail(w, 400, fmt.Errorf("日志时间范围无效"))
			return
		}
		args = append(args, "--since", v)
	}
	level := r.URL.Query().Get("priority")
	if level != "" && level != "all" {
		if level != "err" && level != "warning" && level != "info" {
			fail(w, 400, fmt.Errorf("日志级别无效"))
			return
		}
		args = append(args, "--priority", level)
	}
	script := "exec " + systemdCLI(r.Context(), "journalctl")
	for _, a := range args {
		script += " " + shQuote(a)
	}
	text, e := systemdRead(r.Context(), run, script+"\n")
	if e != nil {
		fail(w, 403, e)
		return
	}
	respond(w, map[string]any{"text": strings.TrimRight(text, "\n"), "lines": lines})
}
