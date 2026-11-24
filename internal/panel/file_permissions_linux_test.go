package panel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilePermissionsRecursiveSkipsSymlinkTargets(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "folder/child"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"folder/child/file", "outside"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../../outside", filepath.Join(dir, "folder/child/link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = applyFilePermissions(context.Background(), root, "folder", "2750", os.Getuid(), os.Getgid(), true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"folder", "folder/child", "folder/child/file"} {
		st, err := root.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if unixPermission(st.Mode()) != 02750 {
			t.Fatalf("%s: %o", name, unixPermission(st.Mode()))
		}
	}
	st, _ := root.Stat("outside")
	if st.Mode().Perm() != 0600 {
		t.Fatal("symlink target changed")
	}
	for _, name := range []string{"folder/child/link", "."} {
		if err = applyFilePermissions(context.Background(), root, name, "0777", os.Getuid(), os.Getgid(), true); err == nil {
			t.Fatalf("unsafe target accepted: %s", name)
		}
	}
	if err = applyFilePermissions(context.Background(), root, "outside", "8888", os.Getuid(), os.Getgid(), false); err == nil {
		t.Fatal("invalid octal accepted")
	}
}

func TestFileIdentitiesExcludeCredentials(t *testing.T) {
	result := fileIdentities("app:private-user-marker:1001:1002:Name:/home/app:/bin/bash\ninvalid\n", "team:private-group-marker:1002:app\n")
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "/home/app") {
		t.Fatal("unnecessary account fields exposed")
	}
	if !strings.Contains(string(raw), `"uid":1001`) || !strings.Contains(string(raw), `"gid":1002`) {
		t.Fatal(string(raw))
	}
}
