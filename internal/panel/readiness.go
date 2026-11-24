package panel

import (
	"context"
	"database/sql"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var frontendAsset = regexp.MustCompile(`(?:src|href)=["'](/assets/[^"']+)["']`)

func frontendReady(webRoot string) bool {
	root, err := os.OpenRoot(webRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	index, err := root.Open("index.html")
	if err != nil {
		return false
	}
	defer index.Close()
	info, err := index.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 64<<10 {
		return false
	}
	html, err := io.ReadAll(io.LimitReader(index, 64<<10))
	if err != nil {
		return false
	}
	hasScript := false
	for _, match := range frontendAsset.FindAllSubmatch(html, -1) {
		asset := strings.TrimPrefix(string(match[1]), "/")
		if !fs.ValidPath(asset) || strings.ContainsAny(asset, "\\:") {
			return false
		}
		info, err := root.Stat(filepath.FromSlash(asset))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
		file, err := root.Open(filepath.FromSlash(asset))
		if err != nil {
			return false
		}
		file.Close()
		hasScript = hasScript || strings.HasSuffix(asset, ".js")
	}
	return hasScript
}

func readinessHandler(db *sql.DB, webRoot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		var one int
		if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || !frontendReady(webRoot) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			respond(w, map[string]bool{"ready": false})
			return
		}
		respond(w, map[string]bool{"ready": true})
	})
}
