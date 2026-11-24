package panel

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSearchBridgeScopeAndUnavailable(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.fileRoot = t.TempDir()
	if err := os.Mkdir(filepath.Join(cfg.fileRoot, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	requests := make(chan map[string]any, 1)
	socket := sizeFixtureSocket(t, func(c net.Conn) {
		var header [4]byte
		if _, err := io.ReadFull(c, header[:]); err != nil {
			return
		}
		payload := make([]byte, binary.LittleEndian.Uint32(header[:]))
		_, _ = io.ReadFull(c, payload)
		var request map[string]any
		_ = json.Unmarshal(payload, &request)
		requests <- request
		reply, _ := json.Marshal(map[string]any{"ok": true, "stale": true, "initializing": true, "results": []map[string]any{
			{"path": filepath.Join(cfg.fileRoot, "docs", "report.txt"), "name": "report.txt", "size_known": true, "logical_size": 0},
			{"path": filepath.Join(cfg.fileRoot, "docs2", "report.txt"), "name": "report.txt"},
			{"path": "/outside/report.txt", "name": "report.txt"},
		}})
		binary.LittleEndian.PutUint32(header[:], uint32(len(reply)))
		_, _ = c.Write(header[:])
		_, _ = c.Write(reply)
	})
	t.Setenv("OWNPANEL_FILESEARCH_SOCKET", socket)
	w := httptest.NewRecorder()
	handleFileSearch(w, httptest.NewRequest("GET", "/api/files/search?q=report&path=/docs", nil))
	request := <-requests
	if request["command"] != "search" || request["scope_path"] != filepath.Join(cfg.fileRoot, "docs") || request["compact"] != true {
		t.Fatal(request)
	}
	var result struct {
		Available bool          `json:"available"`
		Stale     bool          `json:"stale"`
		Results   []indexedFile `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Available || !result.Stale || len(result.Results) != 1 || result.Results[0].Path != "/docs/report.txt" || result.Results[0].Bytes == nil || *result.Results[0].Bytes != 0 {
		t.Fatal(w.Body.String())
	}
	for _, target := range []string{"/api/files/search?q=&path=/", "/api/files/search?q=a&path=/../outside"} {
		w = httptest.NewRecorder()
		handleFileSearch(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	t.Setenv("OWNPANEL_FILESEARCH_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	w = httptest.NewRecorder()
	handleFileSearch(w, httptest.NewRequest("GET", "/api/files/search?q=a&path=/", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Available {
		t.Fatal(w.Body.String(), err)
	}
}
