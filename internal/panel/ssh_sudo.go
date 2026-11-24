package panel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Authenticate before handing stdin to the command (particularly SFTP). A cached
// or NOPASSWD sudo must never cause a password to become shell input or a packet.
type sudoOutput struct {
	mu            sync.Mutex
	dest          io.Writer
	prompt, ready string
	pending       string
	buffered      bytes.Buffer
	active        atomic.Bool
	events        chan string
}

func (f *sudoOutput) flush() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buffered.Len() > 0 {
		if _, err := f.dest.Write(f.buffered.Bytes()); err != nil {
			return err
		}
		f.buffered.Reset()
	}
	if f.pending != "" {
		if _, err := io.WriteString(f.dest, f.pending); err != nil {
			return err
		}
		f.pending = ""
	}
	return nil
}
func (f *sudoOutput) Write(data []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active.Load() {
		if f.buffered.Len() > 0 {
			if _, err := f.dest.Write(f.buffered.Bytes()); err != nil {
				return 0, err
			}
			f.buffered.Reset()
		}
		if f.pending != "" {
			if _, err := io.WriteString(f.dest, f.pending); err != nil {
				return 0, err
			}
			f.pending = ""
		}
		return f.dest.Write(data)
	}
	f.pending += string(data)
	for {
		at, kind, marker := -1, "", ""
		for _, entry := range [][2]string{{"prompt", f.prompt}, {"ready", f.ready}} {
			if index := strings.Index(f.pending, entry[1]); index >= 0 && (at < 0 || index < at) {
				at, kind, marker = index, entry[0], entry[1]
			}
		}
		if at < 0 {
			keep := max(len(f.prompt), len(f.ready)) - 1
			if len(f.pending) > keep {
				f.buffered.WriteString(f.pending[:len(f.pending)-keep])
				f.pending = f.pending[len(f.pending)-keep:]
			}
			break
		}
		f.buffered.WriteString(f.pending[:at])
		f.pending = f.pending[at+len(marker):]
		select {
		case f.events <- kind:
		default:
		}
		if kind == "ready" {
			break
		}
	}
	if f.buffered.Len()+len(f.pending) > 65536 {
		return 0, fmt.Errorf("sudo 握手输出异常")
	}
	return len(data), nil
}

func (d *sshDriver) sudoPassword() string {
	if d.secret.SudoPassword != "" {
		return d.secret.SudoPassword
	}
	return d.secret.Password
}
func (d *sshDriver) passwordSudo() bool {
	return d.n.Sudo && d.n.SudoMode == "password" && d.n.User != "root"
}

func (d *sshDriver) startPasswordSudo(ctx context.Context, session *ssh.Session, target string, pty bool) (io.WriteCloser, <-chan error, error) {
	d.mu.Lock()
	blocked := d.sudoAuthBlocked
	d.mu.Unlock()
	if blocked {
		return nil, nil, fmt.Errorf("此前 sudo 密码验证失败，自动重试已停止，请更新提权设置")
	}
	input, err := session.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	events := make(chan string, 8)
	nonce := token()
	prompt := "OWNPANEL_SUDO_PASSWORD_" + nonce
	ready := "OWNPANEL_SUDO_READY_" + nonce
	wrap := func(dest io.Writer) *sudoOutput {
		if dest == nil {
			dest = io.Discard
		}
		return &sudoOutput{dest: dest, prompt: prompt, ready: ready, events: events}
	}
	filters := []*sudoOutput{wrap(session.Stderr)}
	session.Stderr = filters[0]
	if pty {
		filter := wrap(session.Stdout)
		session.Stdout = filter
		filters = append(filters, filter)
	}
	wrapper := "printf '%s' " + shQuote(ready) + " >&2; exec " + target
	command := "sudo -k -S -p " + shQuote(prompt) + " -- /bin/sh -c " + shQuote(wrapper)
	if pty {
		command = "stty -echo && " + command
	}
	if err = session.Start(command); err != nil {
		input.Close()
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	sent := false
	for {
		select {
		case event := <-events:
			if event == "prompt" {
				if sent {
					d.mu.Lock()
					d.sudoAuthBlocked = true
					d.mu.Unlock()
					input.Close()
					session.Close()
					return nil, nil, fmt.Errorf("sudo 密码错误或无法完成密码验证")
				}
				if _, err = io.WriteString(input, d.sudoPassword()+"\n"); err != nil {
					input.Close()
					session.Close()
					return nil, nil, fmt.Errorf("sudo 验证连接中断")
				}
				sent = true
			} else {
				for _, filter := range filters {
					filter.active.Store(true)
					go func(f *sudoOutput) { _ = f.flush() }(filter)
				}
				return input, done, nil
			}
		case <-done:
			if sent {
				d.mu.Lock()
				d.sudoAuthBlocked = true
				d.mu.Unlock()
			}
			input.Close()
			session.Close()
			return nil, nil, fmt.Errorf("sudo 提权失败，请检查密码与账号的 sudo 权限")
		case <-ctx.Done():
			input.Close()
			session.Close()
			return nil, nil, ctx.Err()
		case <-timer.C:
			input.Close()
			session.Close()
			return nil, nil, fmt.Errorf("sudo 密码验证超时")
		}
	}
}
