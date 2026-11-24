package panel

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func runAgent() {
	cfg.fileRoot = "/" // File management always operates as root over the complete filesystem.
	if e := os.MkdirAll(cfg.fileRoot, 0755); e != nil {
		log.Fatal(e)
	}
	if e := initNginx(); e != nil {
		log.Fatal(e)
	}
	mux := http.NewServeMux()
	registerSystem(mux)
	registerFiles(mux)
	recoverACMEStatuses()
	registerNginx(mux)
	registerFirewall(mux)
	registerIPTables(mux)
	registerTerminal(mux)
	registerSystemd(mux)
	registerDocker(mux)
	startSiteRenewals()
	if certificateLineages != "" {
		watchCertificateLineages(certificateLineages)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/gateway/v1/api/") {
			id, err := strconv.ParseInt(r.Header.Get("X-OwnPanel-User"), 10, 64)
			if err != nil || id <= 0 {
				w.WriteHeader(403)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), panelUserKey{}, id))
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/gateway/v1")
			r.URL.RawPath = ""
		}
		mux.ServeHTTP(w, r)
	})
	if cfg.agentURL != "" || cfg.enrollmentFile != "" {
		if err := startMetricsSpool(mux); err != nil {
			log.Fatal(err)
		}
		runOutboundAgent(handler)
		return
	}

	if e := os.MkdirAll(filepath.Dir(cfg.socket), 0755); e != nil {
		log.Fatal(e)
	}
	if st, e := os.Lstat(cfg.socket); e == nil {
		if st.Mode()&os.ModeSocket == 0 {
			log.Fatal("refuse replacing non-socket")
		}
		c, e := net.DialTimeout("unix", cfg.socket, time.Second)
		if e == nil {
			c.Close()
			log.Fatal("agent already running")
		}
		if e = os.Remove(cfg.socket); e != nil {
			log.Fatal(e)
		}
	}
	l, e := net.Listen("unix", cfg.socket)
	if e != nil {
		log.Fatal(e)
	}
	defer l.Close()
	if e = os.Chmod(cfg.socket, 0600); e != nil {
		log.Fatal(e)
	}
	if cfg.socketGID >= 0 {
		if e = os.Chown(cfg.socket, 0, cfg.socketGID); e != nil {
			log.Fatal(e)
		}
		if e = os.Chmod(cfg.socket, 0660); e != nil {
			log.Fatal(e)
		}
	}
	log.Print("Agent ready: ", cfg.socket)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			audit("request", map[string]string{"method": r.Method, "path": r.URL.Path})
		}
		handler.ServeHTTP(w, r)
	})
	log.Fatal((&http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}).Serve(l))
}
