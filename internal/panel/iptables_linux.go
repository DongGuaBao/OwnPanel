package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func sharedFirewallDirectory() string {
	if root := os.Getenv("OWNPANEL_FIREWALL_ROOT"); root != "" {
		return root
	}
	return "/var/lib/ownpanel-ssh-firewall"
}

func lockSharedFirewall(ctx context.Context) (func(), error) {
	sharedFirewallRoot := sharedFirewallDirectory()
	if err := os.MkdirAll(sharedFirewallRoot, 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(sharedFirewallRoot, "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EAGAIN && err != syscall.EWOULDBLOCK {
			lock.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			lock.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	release := func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); _ = lock.Close() }
	var state struct {
		Phase string `json:"phase"`
	}
	data, err := os.ReadFile(filepath.Join(sharedFirewallRoot, "pending.json"))
	if err == nil {
		if err = json.Unmarshal(data, &state); err != nil {
			release()
			return nil, err
		}
		if state.Phase == "pending" || state.Phase == "armed" || state.Phase == "recovering" {
			release()
			return nil, fmt.Errorf("请先确认或等待 iptables/SSH 防火墙事务恢复")
		}
	} else if !os.IsNotExist(err) {
		release()
		return nil, err
	}
	var lease struct {
		Path string `json:"path"`
	}
	data, err = os.ReadFile(filepath.Join(sharedFirewallRoot, "native-lease.json"))
	if err == nil {
		if err = json.Unmarshal(data, &lease); err != nil {
			release()
			return nil, err
		}
		if lease.Path != pendingPath() {
			if _, err = os.Stat(lease.Path); err == nil {
				release()
				return nil, fmt.Errorf("另一 Agent 的 UFW 变更尚未完成")
			} else if !os.IsNotExist(err) {
				release()
				return nil, err
			}
		}
	} else if !os.IsNotExist(err) {
		release()
		return nil, err
	}
	data, _ = json.Marshal(map[string]string{"path": pendingPath()})
	if err = replacePasswordHash(filepath.Join(sharedFirewallRoot, "native-lease.json"), data); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func registerIPTables(mux *http.ServeMux) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		request, err := iptablesRequest(w, r)
		if err != nil {
			fail(w, 400, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "python3", "-")
		cmd.Stdin = strings.NewReader(firewallPython(request))
		out, err := cmd.CombinedOutput()
		if err != nil {
			fail(w, 400, fmt.Errorf("iptables 操作失败: %w: %s", err, strings.TrimSpace(string(out))))
			return
		}
		firewallResult(w, string(out))
		if r.Method == http.MethodPost {
			audit("iptables_change", map[string]any{"action": request["action"], "program": request["program"]})
		}
	}
	mux.HandleFunc("GET /api/iptables", handler)
	mux.HandleFunc("POST /api/iptables", handler)
	mux.HandleFunc("POST /api/iptables/confirm", handler)
}
