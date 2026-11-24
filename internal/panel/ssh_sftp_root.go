package panel

import (
	"context"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"io"
)

type sftpSessionInput struct {
	io.WriteCloser
	session *ssh.Session
}

func (s *sftpSessionInput) Close() error {
	err := s.WriteCloser.Close()
	_ = s.session.Close()
	return err
}

func (d *sshDriver) openSFTP(ctx context.Context, client *ssh.Client) (*sftp.Client, error) {
	d.mu.Lock()
	loginRoot := d.loginRoot
	d.mu.Unlock()
	if loginRoot {
		return sftp.NewClient(client, sftp.MaxPacket(32<<10))
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}

	if d.passwordSudo() {
		output, err := session.StdoutPipe()
		if err != nil {
			session.Close()
			return nil, err
		}
		session.Stderr = io.Discard
		target := `/bin/sh -c 'for p in /usr/lib/openssh/sftp-server /usr/lib/ssh/sftp-server /usr/libexec/openssh/sftp-server; do if test -x "$p"; then exec "$p"; fi; done; p=$(command -v sftp-server) && exec "$p"; exit 127'`
		input, _, err := d.startPasswordSudo(ctx, session, target, false)
		if err != nil {
			session.Close()
			return nil, err
		}
		result, err := sftp.NewClientPipe(output, &sftpSessionInput{input, session}, sftp.MaxPacket(32<<10))
		if err != nil {
			session.Close()
		}
		return result, err
	}
	input, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	output, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	session.Stderr = io.Discard
	err = session.Start(`exec sudo -n -- /bin/sh -c 'for p in /usr/lib/openssh/sftp-server /usr/lib/ssh/sftp-server /usr/libexec/openssh/sftp-server; do if test -x "$p"; then exec "$p"; fi; done; p=$(command -v sftp-server) && exec "$p"; exit 127'`)
	if err != nil {
		session.Close()
		return nil, err
	}
	result, err := sftp.NewClientPipe(output, &sftpSessionInput{input, session}, sftp.MaxPacket(32<<10))
	if err != nil {
		session.Close()
	}
	return result, err
}
