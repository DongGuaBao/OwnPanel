package panel

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

func registerSystemd(mux *http.ServeMux) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		serveHostSystemd(w, r.WithContext(ctx), func(ctx context.Context, _ bool, script string, args ...string) (string, error) {
			return commandWithInput(ctx, strings.NewReader(script), "/bin/sh", append([]string{"-s", "--"}, args...)...)
		}, os.Geteuid() == 0, "")
	}
	mux.HandleFunc("/api/systemd", handler)
	mux.HandleFunc("/api/systemd/", handler)
}
