package panel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTerminalDetachedReplayBound(t *testing.T) {
	s := &terminalSession{}
	block := bytes.Repeat([]byte("a"), 3<<20)
	if n, e := s.Write(block); e != nil || n != len(block) {
		t.Fatal(n, e)
	}
	if len(s.output) != 2<<20 {
		t.Fatal(len(s.output))
	}
	s.Write([]byte("last"))
	if !bytes.HasSuffix(s.output, []byte("last")) || len(s.output) != 2<<20 {
		t.Fatal("replay bound or order")
	}
	s.finish()
	if !s.info()["ended"].(bool) {
		t.Fatal("exit not recorded")
	}
}

type discardInput struct{}

func (discardInput) Write(p []byte) (int, error) { return len(p), nil }
func (discardInput) Close() error                { return nil }

var _ io.WriteCloser = discardInput{}

func TestTerminalHubLifecycle(t *testing.T) {
	stopped := 0
	h := &terminalHub{start: func(_ context.Context, s *terminalSession) error {
		s.stdin = discardInput{}
		s.stop = func() { stopped++ }
		return nil
	}}
	a, e := h.create(context.Background(), "one")
	if e != nil {
		t.Fatal(e)
	}
	b, e := h.create(context.Background(), "two")
	if e != nil || a.ID == b.ID {
		t.Fatal("session isolation", e)
	}
	h.close()
	if stopped != 2 {
		t.Fatal(stopped)
	}
	if _, e := h.create(context.Background(), "closed"); e == nil {
		t.Fatal("closed hub created shell")
	}
	failed := &terminalHub{start: func(context.Context, *terminalSession) error { return errors.New("no shell") }}
	if _, e := failed.create(context.Background(), "bad"); e == nil || len(failed.sessions) != 0 {
		t.Fatal("failed session retained")
	}
}

func TestTerminalRenameSharedSession(t *testing.T) {
	s := &terminalSession{ID: "existing", Name: "old", output: []byte("kept")}
	h := &terminalHub{sessions: map[string]*terminalSession{"existing": s}}
	for _, tc := range []struct {
		body string
		code int
	}{{`{"name":" 数据库维护 "}`, 200}, {`{"name":" "}`, 400}, {`{"name":"` + strings.Repeat("字", 61) + `"}`, 400}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/terminals/existing", strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/terminals", nil))
	if !strings.Contains(w.Body.String(), "数据库维护") || string(s.output) != "kept" || s.Ended {
		t.Fatal("rename must share name without altering session", w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PATCH", "/api/terminals/missing", strings.NewReader(`{"name":"valid"}`)))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
