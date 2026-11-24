package panel

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"
)

var systemdAccount = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}\$?$`)

type systemdUserContext struct{}

func systemdUserScope(ctx context.Context) bool {
	return ctx.Value(systemdUserContext{}) == true
}

func systemdCLI(ctx context.Context, binary string) string {
	if systemdUserScope(ctx) {
		return binary + " --user"
	}
	return binary
}

// Use the selected account's bus and run filesystem operations as that account.
func userSystemdRunner(run systemdRunner, account string, canSwitch bool) systemdRunner {
	return func(ctx context.Context, _ bool, script string, args ...string) (string, error) {
		delimiter := "OWNPANEL_USER_SYSTEMD"
		for strings.Contains(script, delimiter) {
			delimiter += "_"
		}
		return run(ctx, canSwitch, `set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
account=$1
shift
run_as_account() {
uid=$(id -u -- "$account")
runtime=/run/user/$uid
if ! test -S "$runtime/bus"; then
    echo "此用户的 systemd 实例未运行" >&2
    exit 1
fi
if [ "$uid" = "$(id -u)" ]; then
    unset XDG_CONFIG_HOME
    exec /usr/bin/env LC_ALL=C XDG_RUNTIME_DIR="$runtime" DBUS_SESSION_BUS_ADDRESS="unix:path=$runtime/bus" /bin/sh -s -- "$@"
fi
if [ "$(id -u)" != 0 ]; then
    echo "只能管理 SSH 登录用户的 systemd 实例" >&2
    exit 1
fi
exec /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin runuser -u "$account" -- /usr/bin/env LC_ALL=C XDG_RUNTIME_DIR="$runtime" DBUS_SESSION_BUS_ADDRESS="unix:path=$runtime/bus" /bin/sh -s -- "$@"
}
run_as_account "$@" <<'`+delimiter+"'\n"+script+"\n"+delimiter+"\n", append([]string{account}, args...)...)
	}
}

func serveHostSystemd(w http.ResponseWriter, r *http.Request, run systemdRunner, canSwitch bool, login string) {
	ctx := r.Context()
	if r.URL.Path == "/api/systemd/users" && r.Method == http.MethodGet {
		script := `uid=$(id -u)
for socket in /run/user/*/bus; do
    test -S "$socket" || continue
    id=${socket%/bus}
    id=${id##*/}
    if [ "$#" = 0 ] && [ "$id" != "$uid" ]; then continue; fi
    getent passwd "$id" | cut -d: -f1
done
`
		var args []string
		if canSwitch {
			args = []string{"all"}
		}
		out, err := run(ctx, canSwitch, script, args...)
		if err != nil && canSwitch {
			out, err = run(ctx, false, script)
		}
		if err != nil {
			fail(w, 502, err)
			return
		}
		users := []string{}
		for _, name := range strings.Fields(out) {
			if systemdAccount.MatchString(name) {
				users = append(users, name)
			}
		}
		respond(w, map[string]any{"users": users})
		return
	}
	if suffix, ok := strings.CutPrefix(r.URL.Path, "/api/systemd/user/"); ok {
		account, endpoint, _ := strings.Cut(suffix, "/")
		if !systemdAccount.MatchString(account) {
			fail(w, 400, fmt.Errorf("Linux 用户名无效"))
			return
		}
		r = r.Clone(context.WithValue(ctx, systemdUserContext{}, true))
		r.URL.Path = "/api/systemd"
		if endpoint != "" {
			r.URL.Path += "/" + endpoint
		}
		run = userSystemdRunner(run, account, canSwitch && account != login)
	}
	serveSystemd(w, r, run)
}

func systemdConfigDir(ctx context.Context, run systemdRunner) (string, error) {
	if !systemdUserScope(ctx) {
		return "/etc/systemd/system", nil
	}
	home, err := run(ctx, false, "printf '%s' \"$HOME\"\n")
	if err != nil {
		return "", err
	}
	if !path.IsAbs(home) || strings.ContainsAny(home, "\x00\r\n") {
		return "", fmt.Errorf("用户主目录无效")
	}
	return path.Join(home, ".config/systemd/user"), nil
}

func systemdPreferenceScope(w http.ResponseWriter, r *http.Request, node string) (string, bool) {
	account := r.URL.Query().Get("user")
	if account == "" {
		return node, true
	}
	if !systemdAccount.MatchString(account) {
		fail(w, 400, fmt.Errorf("Linux 用户名无效"))
		return "", false
	}
	return node + ":" + account, true
}
