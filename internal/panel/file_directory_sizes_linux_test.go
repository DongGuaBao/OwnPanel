package panel

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sizeFixtureSocket(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	directory, err := os.MkdirTemp(os.Getenv("TMPDIR"), "fsipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, e := listener.Accept()
			if e != nil {
				return
			}
			go func() { defer connection.Close(); serve(connection) }()
		}
	}()
	return socket
}
func readSizeFixture(connection net.Conn) []string {
	var header [4]byte
	if _, err := io.ReadFull(connection, header[:]); err != nil {
		return nil
	}
	payload := make([]byte, binary.LittleEndian.Uint32(header[:]))
	if _, err := io.ReadFull(connection, payload); err != nil {
		return nil
	}
	var request struct {
		Command string   `json:"command"`
		Paths   []string `json:"paths"`
	}
	_ = json.Unmarshal(payload, &request)
	if request.Command != "directory_sizes" {
		return nil
	}
	return request.Paths
}
func sendSizeFixture(connection net.Conn, paths []string) {
	entries := []map[string]any{}
	for _, path := range paths {
		entries = append(entries, map[string]any{"path": path, "logical_bytes": uint64(4096), "allocated_bytes": 4096, "files": 2, "complete": true, "state": "ready", "updated_at_ms": 12345})
	}
	payload, _ := json.Marshal(map[string]any{"ok": true, "entries": entries})
	header := make([]byte, 4)
	binary.LittleEndian.PutUint32(header, uint32(len(payload)))
	for _, b := range header {
		_, _ = connection.Write([]byte{b})
	}
	_, _ = connection.Write(payload)
}
func TestFileSearchSizeProtocolFragmentedFrames(t *testing.T) {
	socket := sizeFixtureSocket(t, func(c net.Conn) { sendSizeFixture(c, readSizeFixture(c)) })
	result, err := queryFileSearchSizes(context.Background(), socket, []string{"/home/a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].LogicalBytes == nil || *result[0].LogicalBytes != 4096 || result[0].State != "ready" {
		t.Fatalf("unexpected sizes: %+v", result)
	}
}
func TestFileSearchSizeProtocolRejectsOversizedResponse(t *testing.T) {
	socket := sizeFixtureSocket(t, func(c net.Conn) {
		_ = readSizeFixture(c)
		var h [4]byte
		binary.LittleEndian.PutUint32(h[:], 2*1024*1024)
		_, _ = c.Write(h[:])
	})
	if _, err := queryFileSearchSizes(context.Background(), socket, []string{"/a"}); err == nil {
		t.Fatal("oversized response accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := queryFileSearchSizes(ctx, socket, []string{"/a"}); err == nil {
		t.Fatal("cancelled query accepted")
	}
}
func TestDirectorySizesPaginationAndUnavailableService(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.fileRoot = t.TempDir()
	for i := 0; i < 130; i++ {
		if err := os.Mkdir(filepath.Join(cfg.fileRoot, fmt.Sprintf("d%03d", i)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(cfg.fileRoot, "d000"), filepath.Join(cfg.fileRoot, "link")); err != nil {
		t.Fatal(err)
	}
	socket := sizeFixtureSocket(t, func(c net.Conn) { sendSizeFixture(c, readSizeFixture(c)) })
	t.Setenv("OWNPANEL_FILESEARCH_SOCKET", socket)
	response := httptest.NewRecorder()
	handleDirectorySizes(response, httptest.NewRequest("GET", "/api/files/directory-sizes?path=/", nil))
	var result struct {
		Available bool                     `json:"available"`
		Entries   map[string]directorySize `json:"entries"`
		Next      string                   `json:"next"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Available || len(result.Entries) != 128 || result.Next != "d127" {
		t.Fatalf("invalid first page: %s", response.Body.String())
	}
	response = httptest.NewRecorder()
	handleDirectorySizes(response, httptest.NewRequest("GET", "/api/files/directory-sizes?path=/&cursor=d127", nil))
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// Unmarshal into a fresh map because encoding/json merges existing map values.
	var page map[string]json.RawMessage
	_ = json.Unmarshal(response.Body.Bytes(), &page)
	var entries map[string]directorySize
	_ = json.Unmarshal(page["entries"], &entries)
	if len(entries) != 2 {
		t.Fatalf("invalid last page: %s", response.Body.String())
	}
	if _, ok := entries["link"]; ok {
		t.Fatal("directory symlink was followed")
	}
	t.Setenv("OWNPANEL_FILESEARCH_SOCKET", filepath.Join(cfg.fileRoot, "missing.sock"))
	response = httptest.NewRecorder()
	handleDirectorySizes(response, httptest.NewRequest("GET", "/api/files/directory-sizes?path=/", nil))
	if response.Code != 200 {
		t.Fatal("missing optional index blocked file browsing")
	}
	var unavailable struct {
		Available bool `json:"available"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &unavailable)
	if unavailable.Available {
		t.Fatal("missing index reported available")
	}
}

func TestSSHDirectorySizesDoesNotConnectOrScan(t *testing.T) {
	for _, endpoint := range []string{"directory-sizes?path=/", "search?q=report&path=/"} {
		response := httptest.NewRecorder()
		(&sshDriver{}).files(response, httptest.NewRequest("GET", "/api/files/"+endpoint, nil), context.Background())
		if response.Code != 200 {
			t.Fatalf("SSH-only index lookup should be optional: %d", response.Code)
		}
		var result struct {
			Available bool `json:"available"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Available {
			t.Fatal("SSH-only node has no index bridge")
		}
	}
}
