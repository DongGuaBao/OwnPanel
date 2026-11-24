package panel

import (
	"bufio"
	"context"
	"fmt"

	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
)

func command(ctx context.Context, name string, args ...string) (string, error) {
	return commandWithInput(ctx, nil, name, args...)
}

func commandWithInput(ctx context.Context, stdin io.Reader, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	b, e := cmd.CombinedOutput()
	if e != nil {
		return string(b), fmt.Errorf("%s: %w: %s", name, e, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}
func registerSystem(mux *http.ServeMux) {
	var mu sync.Mutex
	var cached any
	var cachedAt time.Time
	mux.HandleFunc("GET /api/metrics", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(cachedAt) < time.Second && cached != nil {
			respond(w, cached)
			return
		}
		c, _ := cpu.PercentWithContext(r.Context(), 150*time.Millisecond, false)
		m, _ := mem.VirtualMemoryWithContext(r.Context())
		sw, _ := mem.SwapMemoryWithContext(r.Context())
		h, _ := host.InfoWithContext(r.Context())
		l, _ := load.AvgWithContext(r.Context())
		n, _ := gnet.IOCountersWithContext(r.Context(), true)
		d, _ := disk.UsageWithContext(r.Context(), cfg.fileRoot)
		ioStats, _ := disk.IOCountersWithContext(r.Context())
		pc := 0.0
		if len(c) > 0 {
			pc = c[0]
		}
		cached = map[string]any{"time": time.Now().UnixMilli(), "cpu": pc, "memory": m, "swap": sw, "host": h, "load": l, "disk": d, "diskIO": ioStats, "network": n}
		cachedAt = time.Now()
		respond(w, cached)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		ng := "inactive"
		if nginxRunning() {
			ng = "active"
		}
		ufw, e := exec.LookPath("ufw")
		respond(w, map[string]any{"nginx": strings.TrimSpace(ng), "ufwAvailable": e == nil, "ufwPath": ufw, "fileRoot": cfg.fileRoot, "nginxPrefix": cfg.nginxPrefix, "systemdAvailable": func() bool { _, e := exec.LookPath("systemctl"); return e == nil }(), "agentUID": os.Geteuid(), "version": "0.6.0", "iptablesAvailable": func() bool { _, err := exec.LookPath("iptables-save"); return err == nil }()})
	})
	mux.HandleFunc("GET /api/logs/ssh", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		s, e := command(ctx, "journalctl", "-u", "ssh.service", "-u", "sshd.service", "-n", "300", "--no-pager", "-o", "short-iso")
		if e != nil {
			for _, p := range []string{"/var/log/auth.log", "/var/log/secure"} {
				if f, err := os.Open(p); err == nil {
					defer f.Close()
					s = tail(f, 300, 256<<10)
					e = nil
					break
				}
			}
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		respond(w, map[string]string{"text": s})
	})
	mux.HandleFunc("GET /api/logs/audit", func(w http.ResponseWriter, r *http.Request) {
		f, e := os.Open(cfg.data + "/audit.jsonl")
		if os.IsNotExist(e) {
			respond(w, map[string]string{"text": ""})
			return
		}
		if e != nil {
			fail(w, 500, e)
			return
		}
		defer f.Close()
		respond(w, map[string]string{"text": tail(f, 300, 256<<10)})
	})
}
func tail(f *os.File, n int, max int64) string {
	st, e := f.Stat()
	if e != nil {
		return ""
	}
	offset := st.Size() - max
	if offset < 0 {
		offset = 0
	}
	_, _ = f.Seek(offset, io.SeekStart)
	s := bufio.NewScanner(io.LimitReader(f, max))
	s.Buffer(make([]byte, 4096), int(max))
	lines := []string{}
	for s.Scan() {
		lines = append(lines, s.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}
