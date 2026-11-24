package panel

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkersPreserveUnmanagedText(t *testing.T) {
	source := "# before\nserver {\n  " + certStart + "\n  old;\n  " + certEnd + "\n  # user text\n}\n"
	got, e := replaceMarkedBlock(source, certStart, certEnd, "  new;")
	if e != nil || got != strings.Replace(source, "  old;\n", "  new;\n", 1) {
		t.Fatalf("unsafe replacement: %q %v", got, e)
	}
	for _, s := range []string{certEnd + "\n" + certStart, certStart + "\n" + certStart + "\n" + certEnd, certStart, "# quoted " + certStart + "\n" + certEnd} {
		if _, e := replaceMarkedBlock(s, certStart, certEnd, "new"); e == nil {
			t.Fatalf("accepted malformed markers: %q", s)
		}
	}
}
func TestFileRootRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if e := os.WriteFile(filepath.Join(outside, "secret"), []byte("private"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(dir, "link")); e != nil {
		t.Fatal(e)
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if f, e := r.Open("link/secret"); e == nil {
		f.Close()
		t.Fatal("symlink escaped root")
	}
}
func TestZipTraversalAndBombMetadata(t *testing.T) {
	for _, name := range []string{"../outside", "/absolute", "a/../../outside", "a\\..\\outside"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			f, e := os.Create(filepath.Join(dir, "bad.zip"))
			if e != nil {
				t.Fatal(e)
			}
			z := zip.NewWriter(f)
			w, e := z.Create(name)
			if e != nil {
				t.Fatal(e)
			}
			_, _ = w.Write([]byte("bad"))
			_ = z.Close()
			_ = f.Close()
			r, e := os.OpenRoot(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			if e = extractZip(r, "bad.zip", "output"); e == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, e = os.Stat(filepath.Join(dir, "output")); !os.IsNotExist(e) {
				t.Fatal("partial extraction left behind")
			}
		})
	}
}
func TestProxyInjectionRejected(t *testing.T) {
	for _, upstream := range []string{"http://127.0.0.1; include /tmp/a;", "http://x\n}", "file:///etc/passwd", "http://user:pass@localhost", "http://$host"} {
		s := site{ID: "test", Domains: "localhost", Port: 80, Kind: "proxy", Upstream: upstream}
		if validateSite(s) == nil {
			t.Fatalf("accepted unsafe proxy: %s", upstream)
		}
	}
}
