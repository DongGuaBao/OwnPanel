package panel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemdUserScopeDispatch(t *testing.T) {
	for _, tt := range []struct {
		url, method, body, command string
	}{
		{"/api/systemd/user/alice?view=activity", "GET", "", "exec systemctl --user 'list-units'"},
		{"/api/systemd/user/alice/logs?unit=demo.service", "GET", "", "exec journalctl --user '--no-pager' '--no-hostname' '-o' 'short-iso' '-n' '500' '--user-unit' 'demo.service'"},
		{"/api/systemd/user/alice/action", "POST", `{"unit":"demo.service","action":"restart"}`, "exec systemctl --user 'restart' '--' 'demo.service'"},
		{"/api/systemd/user/alice/config", "POST", `{"unit":"demo.service","action":"create","content":"[Service]\nExecStart=/bin/true"}`, "p='/home/alice/.config/systemd/user/demo.service'"},
	} {
		t.Run(tt.url, func(t *testing.T) {
			found := false
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tt.method, tt.url, strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			serveHostSystemd(w, r, func(_ context.Context, elevated bool, script string, args ...string) (string, error) {
				if elevated || len(args) != 1 || args[0] != "alice" {
					t.Fatalf("expected SSH login user, got elevated=%v args=%q", elevated, args)
				}
				if strings.Contains(script, `printf '%s' "$HOME"`) {
					return "/home/alice", nil
				}
				found = strings.Contains(script, tt.command)
				if strings.HasSuffix(tt.url, "/config") && (!strings.Contains(script, "systemd-analyze --user verify") || !strings.Contains(script, "systemctl --user daemon-reload")) {
					t.Fatalf("configuration uses the wrong manager: %s", script)
				}
				return "[]", nil
			}, true, "alice")
			if w.Code != http.StatusOK || !found {
				t.Fatalf("status=%d matched=%v body=%s", w.Code, found, w.Body.String())
			}
		})
	}
}

func TestSystemdUserScopeRejectsInvalidAccount(t *testing.T) {
	w := httptest.NewRecorder()
	serveHostSystemd(w, httptest.NewRequest("GET", "/api/systemd/user/-invalid", nil), func(context.Context, bool, string, ...string) (string, error) {
		t.Fatal("invalid account reached the command runner")
		return "", nil
	}, true, "alice")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status %d", w.Code)
	}
}
