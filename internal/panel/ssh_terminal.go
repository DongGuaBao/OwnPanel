package panel

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/crypto/ssh"
)

func (d *sshDriver) startTerminal(ctx context.Context, s *terminalSession) error {
	client, e := d.connect(ctx, false)
	if e != nil {
		return e
	}
	session, e := client.NewSession()
	if e != nil {
		d.invalidate(client)
		return e
	}
	success := false
	defer func() {
		if !success {
			session.Close()
		}
	}()
	echo := uint32(1)
	if d.passwordSudo() {
		echo = 0
	}
	if e = session.RequestPty("xterm-256color", int(s.Rows), int(s.Cols), ssh.TerminalModes{ssh.ECHO: echo, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}); e != nil {
		return e
	}
	session.Stdout = s
	session.Stderr = s
	var done <-chan error
	if !d.passwordSudo() {
		s.stdin, e = session.StdinPipe()
		if e != nil {
			return e
		}
	}
	if d.n.Sudo && d.n.User != "root" {
		caps, e := d.Probe(ctx)
		if e != nil {
			return e
		}
		if caps["terminal.root"].Supported {
			if d.passwordSudo() {
				s.stdin, done, e = d.startPasswordSudo(ctx, session, `/bin/sh -c 'stty echo; exec /bin/sh -l'`, true)
			} else {
				e = session.Start("sudo -n -- /bin/sh -l")
			}
		} else if d.passwordSudo() {
			return fmt.Errorf("sudo 提权失败，请检查密码和权限")
		} else {
			e = session.Shell()
		}
	} else {
		e = session.Shell()
	}
	if e != nil {
		return e
	}
	s.resize = func(c, r uint16) error { return session.WindowChange(int(r), int(c)) }
	var once sync.Once
	s.stop = func() { once.Do(func() { _ = session.Close() }) }
	success = true
	go func() {
		if done != nil {
			<-done
		} else {
			_ = session.Wait()
		}
		session.Close()
		s.finish()
	}()
	return nil
}
