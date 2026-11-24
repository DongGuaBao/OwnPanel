package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSpaceScanMissingAndPartialPaths(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.fileRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg.fileRoot, "visible.txt"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerFiles(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/files/scan?path=/missing", nil))
	if response.Code != 400 {
		t.Fatalf("missing scan root reported success: %d %s", response.Code, response.Body)
	}
	if os.Geteuid() != 0 {
		dir := filepath.Join(cfg.fileRoot, "unreadable")
		if err := os.Mkdir(dir, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/files/scan?path=/", nil))
	var result struct {
		Files, Skipped int
		Bytes          int64
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
		t.Fatalf("scan: %d %s", response.Code, response.Body)
	}
	if result.Files != 1 || result.Bytes != 4 {
		t.Fatalf("wrong scan result: %+v", result)
	}
	if os.Geteuid() != 0 && result.Skipped == 0 {
		t.Fatal("unreadable subtree was not reported")
	}
}
