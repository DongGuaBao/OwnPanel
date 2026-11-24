package panel

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type indexedFile struct {
	Name       string  `json:"name"`
	Path       string  `json:"path"`
	Directory  bool    `json:"directory"`
	Symlink    bool    `json:"symlink"`
	Bytes      *uint64 `json:"bytes"`
	ModifiedAt int64   `json:"modifiedAt"`
}

func handleFileSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" || len(query) > 1024 || !utf8.ValidString(query) || strings.ContainsRune(query, 0) {
		fail(w, 400, fmt.Errorf("请输入 1–1024 字节的文件名"))
		return
	}
	scope, err := relative(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	absolute := filepath.Join(cfg.fileRoot, filepath.FromSlash(scope))
	var response struct {
		Stale        bool `json:"stale"`
		Partial      bool `json:"partial"`
		Initializing bool `json:"initializing"`
		Results      []struct {
			Path      string `json:"path"`
			Name      string `json:"name"`
			Directory bool   `json:"is_directory"`
			Symlink   bool   `json:"is_symlink"`
			Known     bool   `json:"size_known"`
			Bytes     uint64 `json:"logical_size"`
			Modified  int64  `json:"modified_ns"`
		} `json:"results"`
	}
	err = queryFileSearch(r.Context(), fileSearchSocket(), map[string]any{"command": "search", "query": query, "scope_path": absolute, "max_results": 200, "phase": "exact_only", "compact": true}, &response)
	result := []indexedFile{}
	if err != nil {
		respond(w, map[string]any{"available": false, "results": result})
		return
	}
	for _, entry := range response.Results {
		if !filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path || !utf8.ValidString(entry.Path) || strings.ContainsRune(entry.Path, 0) {
			continue
		}
		rel, e := filepath.Rel(absolute, entry.Path)
		if e != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		rootRel, e := filepath.Rel(cfg.fileRoot, entry.Path)
		if e != nil || rootRel == ".." || strings.HasPrefix(rootRel, "../") {
			continue
		}
		var bytes *uint64
		if entry.Known && !entry.Directory {
			value := entry.Bytes
			bytes = &value
		}
		result = append(result, indexedFile{Name: filepath.Base(entry.Path), Path: "/" + filepath.ToSlash(rootRel), Directory: entry.Directory, Symlink: entry.Symlink, Bytes: bytes, ModifiedAt: entry.Modified / 1000000})
	}
	respond(w, map[string]any{"available": true, "results": result, "stale": response.Stale, "partial": response.Partial, "initializing": response.Initializing, "limited": len(response.Results) >= 200})
}
