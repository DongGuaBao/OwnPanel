package panel

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
)

type systemdRunner func(context.Context, bool, string, ...string) (string, error)

var systemdUnitName = regexp.MustCompile(`^[A-Za-z0-9_:@.\\-]+\.(service|socket|timer|target|mount|automount|path|slice|scope|device|swap)$`)

func systemdCommand(run systemdRunner, ctx context.Context, root bool, args ...string) (string, error) {
	script := "exec " + systemdCLI(ctx, "systemctl")
	for _, a := range args {
		script += " " + shQuote(a)
	}
	return run(ctx, root, script+"\n")
}
func systemdRevision(content string) string {
	s := sha256.Sum256([]byte(content))
	return hex.EncodeToString(s[:])
}
func serveSystemd(w http.ResponseWriter, r *http.Request, run systemdRunner) {
	ctx := r.Context()
	if r.URL.Path == "/api/systemd/logs" && r.Method == "GET" {
		serveSystemdLogs(w, r, run)
		return
	}
	if r.URL.Path == "/api/systemd" && r.Method == "GET" {
		active, e := systemdCommand(run, ctx, false, "list-units", "--all", "--no-pager", "--output=json")
		if e != nil {
			fail(w, 502, e)
			return
		}
		if r.URL.Query().Get("view") == "activity" {
			var activity []struct {
				Unit   string `json:"unit"`
				Active string `json:"active"`
				Sub    string `json:"sub"`
				Load   string `json:"load"`
			}
			if json.Unmarshal([]byte(active), &activity) != nil {
				fail(w, 502, fmt.Errorf("systemctl JSON 响应无效"))
			} else {
				respond(w, map[string]any{"activity": activity})
			}
			return
		}
		files, e := systemdCommand(run, ctx, false, "list-unit-files", "--no-pager", "--output=json")
		if e != nil {
			fail(w, 502, e)
			return
		}
		var loaded, installed []map[string]any
		if json.Unmarshal([]byte(active), &loaded) != nil || json.Unmarshal([]byte(files), &installed) != nil {
			fail(w, 502, fmt.Errorf("systemctl JSON 响应无效"))
			return
		}
		units := map[string]map[string]any{}
		for _, v := range loaded {
			name, _ := v["unit"].(string)
			if name != "" {
				units[name] = v
			}
		}
		for _, v := range installed {
			name, _ := v["unit_file"].(string)
			if name == "" {
				continue
			}
			name = name[strings.LastIndex(name, "/")+1:]
			if units[name] == nil {
				units[name] = map[string]any{"unit": name, "active": "inactive", "sub": "dead", "load": "not-loaded"}
			}
			units[name]["state"] = v["state"]
		}
		enrichSystemdInventory(ctx, run, units)
		result := []map[string]any{}
		for _, v := range units {
			result = append(result, v)
		}
		sort.Slice(result, func(i, j int) bool { return fmt.Sprint(result[i]["unit"]) < fmt.Sprint(result[j]["unit"]) })
		respond(w, map[string]any{"units": result})
		return
	}
	name := r.URL.Query().Get("unit")
	if r.Method == "GET" && r.URL.Path == "/api/systemd/unit" {
		if !systemdUnitName.MatchString(name) || len(name) > 255 {
			fail(w, 400, fmt.Errorf("单元名称无效"))
			return
		}
		show, e := systemdCommand(run, ctx, false, "show", "--no-pager", "--property=Id,Description,LoadState,ActiveState,SubState,UnitFileState,FragmentPath,DropInPaths,MainPID,ExecMainStatus,MemoryCurrent,TasksCurrent,ActiveEnterTimestamp", "--", name)
		if e != nil {
			fail(w, 400, e)
			return
		}
		properties := map[string]string{}
		for _, line := range strings.Split(show, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				properties[k] = v
			}
		}
		if r.URL.Query().Get("view") == "state" {
			respond(w, map[string]any{"properties": properties})
			return
		}
		config, _ := systemdCommand(run, ctx, true, "cat", "--no-pager", "--", name)
		if config == "" {
			config, _ = systemdCommand(run, ctx, false, "cat", "--no-pager", "--", name)
		}
		logs := ""
		if r.URL.Query().Get("view") != "editor" {
			logs, e = run(ctx, true, "exec "+systemdCLI(ctx, "journalctl")+" --no-pager -o short-iso -n 200 "+systemdLogUnitFlag(ctx)+" "+shQuote(name)+"\n")
			if e != nil {
				logs, e = run(ctx, false, "exec "+systemdCLI(ctx, "journalctl")+" --no-pager -o short-iso -n 200 "+systemdLogUnitFlag(ctx)+" "+shQuote(name)+"\n")
			}
			if e != nil {
				logs = e.Error()
			}
		}
		dir, e := systemdConfigDir(ctx, run)
		if e != nil {
			fail(w, 400, e)
			return
		}
		overridePath := path.Join(dir, name+".d", "ownpanel.conf")
		overrideScript := "p=" + shQuote(overridePath) + "; if test -e \"$p\"; then cat -- \"$p\"; fi\n"
		override, e := run(ctx, true, overrideScript)
		if e != nil {
			override, e = run(ctx, false, overrideScript)
		}
		if e != nil {
			fail(w, 403, e)
			return
		}
		respond(w, map[string]any{"properties": properties, "config": config, "logs": logs, "override": override, "overridePath": overridePath, "revision": systemdRevision(override), "source": systemdSource(ctx, run, name)})
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var body struct {
		Unit     string `json:"unit"`
		Action   string `json:"action"`
		Content  string `json:"content"`
		Revision string `json:"revision"`
		Path     string `json:"path"`
	}
	if e := decode(w, r, &body); e != nil {
		fail(w, 400, e)
		return
	}
	if body.Action != "daemon-reload" && (!systemdUnitName.MatchString(body.Unit) || len(body.Unit) > 255) {
		fail(w, 400, fmt.Errorf("单元名称无效"))
		return
	}
	if r.URL.Path == "/api/systemd/config" {
		if len(body.Content) > 1<<20 || strings.ContainsRune(body.Content, 0) {
			fail(w, 400, fmt.Errorf("配置内容无效"))
			return
		}
		dir, e := systemdConfigDir(ctx, run)
		if e != nil {
			fail(w, 400, e)
			return
		}
		ctl := systemdCLI(ctx, "systemctl")
		target := path.Join(dir, body.Unit+".d", "ownpanel.conf")
		if body.Action == "unit" {
			var e error
			target, e = systemdFragmentPath(ctx, run, body.Unit)
			if e != nil {
				fail(w, 400, e)
				return
			}
			if target != body.Path {
				fail(w, 409, fmt.Errorf("源配置路径已变化，请重新读取"))
				return
			}
		}
		if body.Action == "create" {
			target = path.Join(dir, body.Unit)
		}
		if body.Action != "override" && strings.TrimSpace(body.Content) == "" {
			fail(w, 400, fmt.Errorf("单元配置不能为空"))
			return
		}
		if body.Action != "create" && body.Action != "override" && body.Action != "unit" {
			fail(w, 400, fmt.Errorf("配置操作无效"))
			return
		}
		script := "set -eu\np=" + shQuote(target) + "\nmkdir -p -- \"$(dirname -- \"$p\")\"\nmkdir -p -- " + shQuote(dir) + "\nexec 9> " + shQuote(path.Join(dir, ".ownpanel-config.lock")) + "\nflock -x 9\n"
		if body.Action == "unit" {
			script += "current=$(" + ctl + " show --property=FragmentPath --value -- " + shQuote(body.Unit) + "); current=$(readlink -f -- \"$current\"); if test \"$current\" != \"$p\" || ! test -f \"$p\"; then echo '源配置位置已变化，请重新读取' >&2; exit 1; fi\n"
		}
		if body.Action == "create" {
			script += "if test -e \"$p\" || test -L \"$p\"; then echo '本地配置已存在，未覆盖' >&2; exit 1; fi\nif " + ctl + " show --property=LoadState --value -- " + shQuote(body.Unit) + " | grep -qv '^not-found$'; then echo '单元已存在，请使用覆盖配置' >&2; exit 1; fi\n"
		} else {
			if len(body.Revision) != 64 {
				fail(w, 400, fmt.Errorf("请重新读取配置后编辑"))
				return
			}
			script += "actual=$(if test -e \"$p\"; then cat -- \"$p\"; fi | sha256sum); actual=${actual%% *}\nif test \"$actual\" != " + shQuote(body.Revision) + "; then echo '配置已变化，请重新读取' >&2; exit 1; fi\n"
		}
		if body.Content == "" && body.Action == "override" {
			script += "rm -f -- \"$p\"\n"
		} else {
			script += "t=$(mktemp \"${p}.XXXXXX\"); check=''; trap 'rm -f -- \"$t\"; if test -n \"$check\"; then rm -rf -- \"$check\"; fi' EXIT\nprintf '%s' " + shQuote(base64.StdEncoding.EncodeToString([]byte(body.Content))) + " | base64 -d > \"$t\"\n"
			if body.Action == "create" || body.Action == "unit" {
				checkTemplate := "/run/ownpanel-systemd-check.XXXXXX"
				if systemdUserScope(ctx) {
					checkTemplate = path.Join(dir, ".ownpanel-check.XXXXXX")
				}
				script += "check=$(mktemp -d " + shQuote(checkTemplate) + ")\ncp -- \"$t\" \"$check/" + body.Unit + "\"\n" + systemdCLI(ctx, "systemd-analyze") + " verify --man=no -- \"$check/" + body.Unit + "\"\n"
			}
			if body.Action == "unit" {
				script += "chown --reference=\"$p\" -- \"$t\"\nchmod --reference=\"$p\" -- \"$t\"\n"
			} else {
				script += "chmod 0644 \"$t\"\n"
			}
			script += "mv -f -- \"$t\" \"$p\"\n"
		}
		script += ctl + " daemon-reload\n"
		out, e := run(ctx, true, script)
		if e != nil {
			fail(w, 400, e)
			return
		}
		respond(w, map[string]any{"ok": true, "output": out})
		return
	}
	if r.URL.Path != "/api/systemd/action" {
		w.WriteHeader(404)
		return
	}
	allowed := map[string]bool{"start": true, "stop": true, "restart": true, "reload": true, "enable": true, "disable": true, "mask": true, "unmask": true, "reset-failed": true, "daemon-reload": true}
	if !allowed[body.Action] {
		fail(w, 400, fmt.Errorf("Systemd 操作无效"))
		return
	}
	args := []string{body.Action}
	if body.Action != "daemon-reload" {
		args = append(args, "--", body.Unit)
	}
	out, e := systemdCommand(run, ctx, true, args...)
	if e != nil {
		fail(w, 400, e)
		return
	}
	respond(w, map[string]any{"ok": true, "output": out})
}
