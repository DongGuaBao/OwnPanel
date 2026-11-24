package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var firewallMu sync.Mutex

type firewallPending struct {
	ID           string    `json:"id"`
	Unit         string    `json:"unit"`
	Expires      time.Time `json:"expires"`
	Durable      bool      `json:"durable,omitempty"`
	ConfirmGuard bool      `json:"confirmGuard,omitempty"`
}

func pendingPath() string { return filepath.Join(cfg.data, "firewall-pending.json") }
func readPending() (*firewallPending, error) {
	b, e := os.ReadFile(pendingPath())
	if os.IsNotExist(e) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var p firewallPending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if len(p.ID) != 16 || !validName(p.ID) || p.Unit != "ownpanel-fw-"+p.ID || p.Expires.IsZero() {
		return nil, fmt.Errorf("防火墙恢复记录无效")
	}
	return &p, nil
}
func firewallStatus(ctx context.Context) (string, error) {
	return command(ctx, "ufw", "status", "numbered")
}
func recoverFirewallPending(ctx context.Context) error {
	p, err := readPending()
	if err != nil {
		return err
	}
	if p == nil || !p.Durable {
		return nil
	}
	dir := filepath.Join(cfg.data, "firewall", p.ID)
	if _, err := os.Stat(filepath.Join(dir, "confirmed")); err == nil {
		return os.Remove(pendingPath())
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Stat(filepath.Join(dir, "restored")); os.IsNotExist(err) {
		if time.Now().Before(p.Expires) {
			if _, err := command(ctx, "systemctl", "is-active", "--quiet", p.Unit+".timer"); err == nil {
				return nil
			}
		}
		if _, err := command(ctx, "/bin/bash", filepath.Join(dir, "restore.sh")); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := os.Remove(pendingPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	audit("firewall_recovered", p.ID)
	return nil
}

func registerFirewall(mux *http.ServeMux) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := recoverFirewallPending(ctx); err != nil {
		log.Printf("firewall startup recovery: %v", err)
	}
	cancel()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			firewallMu.Lock()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := recoverFirewallPending(ctx)
			cancel()
			firewallMu.Unlock()
			if err != nil {
				log.Printf("firewall recovery retry: %v", err)
			}
		}
	}()
	mux.HandleFunc("GET /api/firewall", func(w http.ResponseWriter, r *http.Request) {
		firewallMu.Lock()
		defer firewallMu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		s, e := firewallStatus(ctx)
		if e != nil {
			fail(w, 500, e)
			return
		}
		p, pendingErr := readPending()
		recoveryError := ""
		if pendingErr != nil {
			recoveryError = "无法读取防火墙恢复记录，变更已暂停"
		}
		if p != nil && !p.Durable && time.Now().After(p.Expires) {
			p = nil
		}
		added, _ := command(ctx, "ufw", "show", "added")
		respond(w, map[string]any{"text": s, "added": added, "pending": p, "backend": "ufw", "recoveryError": recoveryError})
	})
	mux.HandleFunc("POST /api/firewall", func(w http.ResponseWriter, r *http.Request) {
		var b firewallChange
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		firewallMu.Lock()
		defer firewallMu.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		release, err := lockSharedFirewall(ctx)
		if err != nil {
			fail(w, 409, err)
			return
		}
		defer release()
		if err := recoverFirewallPending(ctx); err != nil {
			fail(w, 409, fmt.Errorf("上一项变更尚未恢复: %w", err))
			return
		}
		if p, err := readPending(); err != nil || p != nil && (p.Durable || time.Now().Before(p.Expires)) {
			fail(w, 409, fmt.Errorf("请先确认或等待上一项变更恢复"))
			return
		}
		added, e := command(ctx, "ufw", "show", "added")
		if e != nil {
			fail(w, 500, e)
			return
		}
		commands, e := firewallCommands(b, added)
		if e != nil {
			fail(w, 400, e)
			return
		}
		status, e := firewallStatus(ctx)
		if e != nil {
			fail(w, 500, e)
			return
		}
		active := strings.Contains(status, "Status: active")
		id := token()[:16]
		unit := "ownpanel-fw-" + id
		dir := filepath.Join(cfg.data, "firewall", id)
		if e = os.MkdirAll(dir, 0700); e != nil {
			fail(w, 500, e)
			return
		}
		archive := filepath.Join(dir, "rules.tar")
		if _, e = command(ctx, "tar", "-cf", archive, "-C", "/", "etc/ufw", "etc/default/ufw"); e != nil {
			fail(w, 500, e)
			return
		}
		restoreAction := "ufw --force enable\nufw reload"
		if !active {
			restoreAction = "ufw disable"
		}
		for _, program := range []string{"iptables", "ip6tables"} {
			if _, err := exec.LookPath(program + "-save"); err == nil {
				raw, err := command(ctx, "python3", "-c", "__name__='ownpanel_capture'\n"+sshFirewallProgram+"\nprint(capture_rules('"+program+"'))")
				if err != nil {
					fail(w, 500, err)
					return
				}
				saved := filepath.Join(dir, program+".rules")
				if err = os.WriteFile(saved, []byte(raw), 0600); err != nil {
					fail(w, 500, err)
					return
				}
				restoreAction += "\n" + program + "-restore --wait 5 --counters < " + shQuote(saved)
			}
		}
		script := filepath.Join(dir, "restore.sh")
		marker := shQuote(filepath.Join(dir, "restored"))
		content := "#!/bin/bash\nset -eu\nexec 9>" + shQuote(filepath.Join(dir, "restore.lock")) + "\nflock -x 9\ntest ! -e " + shQuote(filepath.Join(dir, "confirmed")) + " || exit 0\ntest ! -e " + marker + " || exit 0\ntar -xf " + shQuote(archive) + " -C /\n" + restoreAction + "\ntouch " + marker + "\n"
		if e = os.WriteFile(script, []byte(content), 0700); e != nil {
			fail(w, 500, e)
			return
		}
		if _, e = command(ctx, "systemd-run", "--quiet", "--unit="+unit, "--on-active=60s", "--timer-property=AccuracySec=1s", "/bin/bash", script); e != nil {
			fail(w, 500, fmt.Errorf("恢复任务创建失败，未修改防火墙: %w", e))
			return
		}
		p := firewallPending{ID: id, Unit: unit, Expires: time.Now().Add(60 * time.Second), Durable: true, ConfirmGuard: true}
		pb, _ := json.Marshal(p)
		if e = replacePasswordHash(pendingPath(), pb); e != nil {
			_, _ = command(ctx, "systemctl", "stop", unit+".timer")
			fail(w, 500, e)
			return
		}
		if b.Action == "enable" {
			_, e = command(ctx, "ufw", "allow", "22/tcp")
			if e == nil {
				_, e = command(ctx, "ufw", "allow", "7080/tcp")
			}
		}
		out := ""
		if e == nil {
			for _, args := range commands {
				var result string
				result, e = command(ctx, "ufw", args...)
				out += result
				if e != nil {
					break
				}
			}
		}
		if e != nil {
			recoveryCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
			defer stop()
			if _, restoreErr := command(recoveryCtx, "/bin/bash", script); restoreErr == nil {
				_, _ = command(recoveryCtx, "systemctl", "stop", unit+".timer")
				_ = os.Remove(pendingPath())
			} else {
				log.Printf("immediate firewall rollback failed: %v", restoreErr)
			}
			fail(w, 400, e)
			return
		}
		audit("firewall_change", b)
		respond(w, map[string]any{"output": out, "pending": p})
	})
	mux.HandleFunc("POST /api/firewall/confirm", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			ID string `json:"id"`
		}
		if e := decode(w, r, &b); e != nil {
			fail(w, 400, e)
			return
		}
		firewallMu.Lock()
		defer firewallMu.Unlock()
		p, err := readPending()
		if err != nil || p == nil || p.ID != b.ID || time.Until(p.Expires) < 3*time.Second {
			fail(w, 409, fmt.Errorf("变更已过期或正在恢复，请刷新状态"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := markFirewallConfirmed(ctx, p); err != nil {
			fail(w, 409, err)
			return
		}
		if _, e := command(ctx, "systemctl", "stop", p.Unit+".timer"); e != nil {
			fail(w, 500, e)
			return
		}
		if err := os.Remove(pendingPath()); err != nil {
			fail(w, 500, fmt.Errorf("无法保存确认结果: %w", err))
			return
		}
		audit("firewall_confirm", p.ID)
		respond(w, map[string]bool{"ok": true})
	})
}

// The native recovery process takes the same flock. A durable decision must be
// visible before stopping the timer: its service may already have been started.
func markFirewallConfirmed(ctx context.Context, pending *firewallPending) error {
	if !pending.ConfirmGuard {
		return fmt.Errorf("旧版防火墙事务请等待自动恢复后重试")
	}
	dir := filepath.Join(cfg.data, "firewall", pending.ID)
	lock, err := os.OpenFile(filepath.Join(dir, "restore.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("恢复正在执行，无法确认: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if time.Until(pending.Expires) < 3*time.Second {
		return fmt.Errorf("变更已过期或正在恢复，请刷新状态")
	}
	if _, err := os.Stat(filepath.Join(dir, "restored")); err == nil {
		return fmt.Errorf("变更已经恢复，请刷新状态")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := replacePasswordHash(filepath.Join(dir, "confirmed"), []byte(pending.ID)); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
