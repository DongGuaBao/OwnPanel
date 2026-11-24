package panel

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestGatewayBrowserGrant(t *testing.T) {
	var challenge string
	var audience = gatewayApp
	var allowed = true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Service internal-test" || r.URL.RawQuery != "" {
			t.Error("unexpected gateway credentials or query")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/internal/auth/exchange":
			var payload map[string]string
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&payload) != nil || payload["code"] != strings.Repeat("c", 43) {
				w.WriteHeader(400)
				return
			}
			respond(w, map[string]any{"user_id": 123, "aud": audience, "login_challenge": challenge, "name": "tester"})
		case "/internal/auth/users/123/permissions/ownpanel.admin":
			respond(w, map[string]any{"user_id": 123, "permission": gatewayPermission, "allowed": allowed})
		default:
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	a := publicAccess{gateway: "https://login.test", internal: upstream.URL, credential: "internal-test", client: upstream.Client()}
	mux := http.NewServeMux()
	issued := 0
	a.register(mux, func(_ http.ResponseWriter, _ *http.Request, id int64, name string) {
		if id != 123 || name != "tester" {
			t.Fatal("wrong user")
		}
		issued++
	})
	start := httptest.NewRecorder()
	mux.ServeHTTP(start, httptest.NewRequest("GET", "/auth/start", nil))
	result := start.Result()
	cookies := result.Cookies()
	u, err := result.Location()
	if err != nil || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || u.Host != "login.test" {
		t.Fatal("login initiation")
	}
	expected := u.Query().Get("login_challenge")
	callback := func(want int) {
		t.Helper()
		r := httptest.NewRequest("GET", "/auth/callback?code="+strings.Repeat("c", 43), nil)
		r.AddCookie(cookies[0])
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("callback got %d want %d", w.Code, want)
		}
	}
	challenge = "unbound"
	callback(403)
	challenge = expected
	audience = "other"
	callback(403)
	audience = gatewayApp
	allowed = false
	callback(403)
	allowed = true
	callback(303)
	if issued != 1 {
		t.Fatalf("issued %d sessions", issued)
	}
}

func TestTrustedPublicOrigin(t *testing.T) {
	a := publicAccess{origin: "https://panel.example:8443", proxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	r := httptest.NewRequest("POST", "http://backend/api/login", nil)
	r.Header.Set("Origin", a.origin)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Real-IP", "192.0.2.4")
	if !a.trusted(r) || !a.originOK(r) || a.source(r) != "192.0.2.4" {
		t.Fatal("trusted proxy failed")
	}
	r.RemoteAddr = "192.0.2.1:443"
	r.Header.Set("Origin", "http://backend")
	r.Header.Set("X-Forwarded-Proto", "https")
	if a.trusted(r) || a.originOK(r) || a.source(r) != "192.0.2.1" {
		t.Fatal("accepted external headers")
	}
}

func TestTerminalOwnerIsolation(t *testing.T) {
	h := terminalHub{start: func(context.Context, *terminalSession) error { return nil }}
	ctx := context.WithValue(context.Background(), panelUserKey{}, int64(7))
	s, err := h.create(ctx, "private")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/api/terminals", 200}, {"GET", "/api/terminal?session=" + s.ID, 404}, {"PATCH", "/api/terminals/" + s.ID, 404}, {"DELETE", "/api/terminals/" + s.ID, 404}} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"name":"new"}`))
		r = r.WithContext(context.WithValue(r.Context(), panelUserKey{}, int64(8)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), s.ID) || h.sessions[s.ID] != s || s.Name != "private" {
			t.Errorf("%s %s failed isolation", tc.method, tc.path)
		}
	}
}

func TestFileOperationBudgets(t *testing.T) {
	w := httptest.NewRecorder()
	previewFile(w, bytes.NewReader(make([]byte, maxPreviewBytes+1)), 0)
	if w.Code != 413 {
		t.Fatal("growing file escaped preview limit")
	}
	z := &zip.File{FileHeader: zip.FileHeader{Name: "file", UncompressedSize64: maxExpandedBytes}}
	if validateZIPEntries([]*zip.File{z, z}) == nil || validateZIPEntries(make([]*zip.File, maxZIPEntries+1)) == nil {
		t.Fatal("archive budget")
	}
	if copyZIPEntry(io.Discard, strings.NewReader("long"), 1) == nil {
		t.Fatal("unbounded entry")
	}
}
