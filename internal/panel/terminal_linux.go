package panel

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

func registerTerminal(mux *http.ServeMux) {
	h := &terminalHub{start: func(_ context.Context, s *terminalSession) error {
		cmd := exec.Command("/bin/bash", "-l")
		cmd.Env = append(os.Environ(), "TERM=xterm-256color", "HOME=/root", "USER=root")
		cmd.Dir = "/root"
		pt, e := pty.StartWithSize(cmd, &pty.Winsize{Rows: s.Rows, Cols: s.Cols})
		if e != nil {
			return e
		}
		s.stdin = pt
		s.resize = func(c, r uint16) error { return pty.Setsize(pt, &pty.Winsize{Cols: c, Rows: r}) }
		var once sync.Once
		s.stop = func() {
			once.Do(func() { _ = cmd.Process.Signal(syscall.SIGHUP); _ = pt.Close(); _ = cmd.Process.Kill() })
		}
		go func() {
			buf := make([]byte, 16<<10)
			for {
				n, e := pt.Read(buf)
				if n > 0 {
					_, _ = s.Write(buf[:n])
				}
				if e != nil {
					break
				}
			}
			_ = cmd.Wait()
			_ = pt.Close()
			s.finish()
		}()
		return nil
	}}
	mux.Handle("/api/terminals", h)
	mux.Handle("/api/terminals/", h)
	mux.Handle("GET /api/terminal", h)
}
