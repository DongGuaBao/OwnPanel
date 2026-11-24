package panel

import (
	"context"
	"github.com/pkg/sftp"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestSSHFilePermissionsRecursiveSkipsLinks(t *testing.T) {
	temp := t.TempDir()
	folder := filepath.Join(temp, "selected")
	if err := os.Mkdir(folder, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join(folder, "file"), filepath.Join(temp, "outside")} {
		if err := os.WriteFile(name, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(temp, "outside"), filepath.Join(folder, "link")); err != nil {
		t.Fatal(err)
	}
	clientConn, serverConn := net.Pipe()
	server, err := sftp.NewServer(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	defer serverConn.Close()
	go server.Serve()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer clientConn.Close()
	if err = (&sshDriver{}).applySSHFilePermissions(context.Background(), client, folder, 0750, os.Getuid(), os.Getgid(), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{folder, filepath.Join(folder, "file")} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != 0750 {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	info, _ := os.Stat(filepath.Join(temp, "outside"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("followed a symlink")
	}
}
