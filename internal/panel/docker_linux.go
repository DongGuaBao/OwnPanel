package panel

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

func localDockerEngines() []dockerEngine {
	items := []dockerEngine{}
	if _, err := exec.LookPath("docker"); err != nil {
		return items
	}
	sockets, _ := filepath.Glob("/run/user/*/docker.sock")
	for _, socket := range sockets {
		uid := filepath.Base(filepath.Dir(socket))
		n, err := strconv.Atoi(uid)
		if err != nil || n <= 0 || strconv.Itoa(n) != uid || (os.Geteuid() != 0 && os.Geteuid() != n) {
			continue
		}
		account, err := user.LookupId(uid)
		if err != nil {
			continue
		}
		if st, e := os.Lstat(socket); e == nil && st.Mode()&os.ModeSocket != 0 {
			items = append(items, dockerEngine{uid, account.Username + " · Rootless"})
		}
	}
	if st, err := os.Stat("/var/run/docker.sock"); err == nil && st.Mode()&os.ModeSocket != 0 {
		items = append(items, dockerEngine{"system", "系统 Docker"})
	}
	return items
}

func localDockerRunner(engine string) dockerRunner {
	return func(ctx context.Context, args ...string) (string, error) {
		socket := "/var/run/docker.sock"
		program := "docker"
		var prefix []string
		if engine != "system" {
			account, err := user.LookupId(engine)
			if err != nil {
				return "", err
			}
			runtimeDir := "/run/user/" + account.Uid
			socket = runtimeDir + "/docker.sock"
			if os.Geteuid() == 0 {
				program = "runuser"
				prefix = []string{"-u", account.Username, "--", "env", "-i", "PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + account.HomeDir, "XDG_RUNTIME_DIR=" + runtimeDir, "docker"}
			}
		}
		prefix = append(prefix, "--host", "unix://"+socket)
		cmd := exec.CommandContext(ctx, program, append(prefix, args...)...)
		out := &boundedOutput{limit: 4 << 20}
		stderr := &boundedOutput{limit: 64 << 10}
		cmd.Stdout = out
		cmd.Stderr = stderr
		if len(args) > 1 && args[1] == "logs" {
			cmd.Stderr = out
		}
		if err := cmd.Run(); err != nil {
			detail := stderr.String()
			if cmd.Stderr == out {
				detail = out.String()
			}
			return "", fmt.Errorf("Docker：%w %s", err, strings.TrimSpace(detail))
		}
		return out.String(), nil
	}
}

func registerDocker(mux *http.ServeMux) {
	mux.HandleFunc("/api/docker/", func(w http.ResponseWriter, r *http.Request) {
		serveDocker(w, r, localDockerEngines(), localDockerRunner)
	})
}
