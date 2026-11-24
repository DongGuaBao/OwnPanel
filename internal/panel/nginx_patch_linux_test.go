package panel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNginxVisualSaveKeepsManualConfiguration(t *testing.T) {
	binary, err := filepath.Abs("../../tools/nginx/sbin/nginx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(binary); err != nil {
		t.Skip("private Nginx not built")
	}
	original := cfg
	t.Cleanup(func() { cfg = original })
	cfg.nginxBinary = binary
	cfg.nginxPrefix = t.TempDir()
	cfg.nginxMimeTypes = filepath.Join(filepath.Dir(filepath.Dir(binary)), "conf/mime.types")
	if err = initNginx(); err != nil {
		t.Fatal(err)
	}
	s := site{ID: "patch-example", Domains: "before.test", Port: 59439, Kind: "locations", Routing: "location / {\n proxy_pass http://127.0.0.1:12345;\n proxy_http_version 1.1;\n}\n"}
	if _, err = applySite(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg.nginxPrefix, "sites", s.ID)
	server := filepath.Join(dir, "server.conf")
	raw, err := os.ReadFile(server)
	if err != nil {
		t.Fatal(err)
	}
	manual := strings.Replace(string(raw), "server {", "server {\n # User directive\n add_header X-Custom preserved;", 1)
	if err = os.WriteFile(server, []byte(manual), 0600); err != nil {
		t.Fatal(err)
	}
	route := filepath.Join(dir, "routing.conf")
	if err = os.WriteFile(route, []byte(strings.Replace(s.Routing, " proxy_http_version 1.1;", " proxy_http_version 1.1;\n proxy_read_timeout 1200s;", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	s.Domains = "after.test"
	s.Routing = "# OWNPANEL ROUTES BEGIN\n" + strings.Replace(s.Routing, " proxy_pass http://127.0.0.1:12345;", " proxy_pass http://127.0.0.1:12346;", 1) + "# OWNPANEL ROUTES END\n"
	if _, err = applySite(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	for name, need := range map[string]string{"server.conf": "add_header X-Custom preserved;", "routing.conf": "proxy_read_timeout 1200s;"} {
		content, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil || !strings.Contains(string(content), need) {
			t.Fatalf("manual directive lost in %s: %s %v", name, content, readErr)
		}
	}
	s.Domains = "again.test"
	if _, err = applySite(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(server)
	if !strings.Contains(string(content), "add_header X-Custom preserved;") || !strings.Contains(string(content), "again.test") {
		t.Fatal(string(content))
	}
}
