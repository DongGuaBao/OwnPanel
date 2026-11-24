package panel

import (
	"strings"
	"testing"
)

func TestNginxPatchPreservesManualDirectives(t *testing.T) {
	base := "server {\n listen 8080;\n server_name old.test;\n}\n"
	current := "# custom heading\nserver {\n listen 8080;\n proxy_read_timeout 600s;\n server_name old.test;\n add_header X-Custom yes;\n}\n# custom footer\n"
	desired := strings.Replace(base, "8080", "9090", 1)
	got, err := patchNginxText(base, current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Replace(current, "8080", "9090", 1) {
		t.Fatalf("manual content changed: %s", got)
	}
	noop, err := patchNginxText(base, current, base)
	if err != nil || noop != current {
		t.Fatal("unchanged form must preserve the exact file", err)
	}
	if _, err = patchNginxText(base, strings.Replace(current, "8080", "7070", 1), desired); err == nil {
		t.Fatal("overlapping manual edits must not be overwritten")
	}
}

func TestNginxPatchReusesManualWebSocketHeaders(t *testing.T) {
	base := "location / {\n proxy_pass http://upstream;\n}\n"
	current := "location / {\n proxy_pass http://upstream;\n proxy_set_header Upgrade $http_upgrade;\n proxy_set_header Connection $connection_upgrade;\n proxy_read_timeout 900s;\n}\n"
	desired := "# OWNPANEL ROUTES BEGIN\nlocation / {\n proxy_pass http://upstream;\n proxy_set_header Upgrade $http_upgrade;\n proxy_set_header Connection $connection_upgrade;\n}\n# OWNPANEL ROUTES END\n"
	got, err := patchNginxText(base, current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(got, "proxy_set_header Upgrade") != 1 || strings.Count(got, "proxy_set_header Connection") != 1 || !strings.Contains(got, "proxy_read_timeout 900s;") {
		t.Fatal(got)
	}
}
