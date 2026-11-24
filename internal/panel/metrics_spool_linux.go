package panel

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"time"
)

func startMetricsSpool(mux *http.ServeMux) error {
	db, err := sql.Open("sqlite", filepath.Join(cfg.data, "metrics-spool.db"))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("CREATE TABLE IF NOT EXISTS samples(ts INTEGER PRIMARY KEY, data TEXT NOT NULL)"); err != nil {
		db.Close()
		return err
	}
	mux.HandleFunc("GET /api/metrics/spool", func(w http.ResponseWriter, r *http.Request) {
		after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		if err != nil {
			fail(w, 400, err)
			return
		}
		rows, err := db.QueryContext(r.Context(), "SELECT data FROM samples WHERE ts>? ORDER BY ts LIMIT 180", after)
		if err != nil {
			fail(w, 500, err)
			return
		}
		defer rows.Close()
		samples := []json.RawMessage{}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				fail(w, 500, err)
				return
			}
			samples = append(samples, json.RawMessage(raw))
		}
		if err := rows.Err(); err != nil {
			fail(w, 500, err)
			return
		}
		respond(w, map[string]any{"samples": samples})
	})
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest("GET", "/api/metrics", nil))
			timestamp, err := metricTime(response.Body.Bytes())
			if err == nil && response.Code == 200 {
				_, err = db.Exec("INSERT OR IGNORE INTO samples VALUES (?,?)", timestamp, response.Body.String())
			}
			if err != nil {
				log.Printf("metrics spool sample: %v", err)
			}
			if _, err := db.Exec("DELETE FROM samples WHERE ts<?", time.Now().Add(-7*24*time.Hour).UnixMilli()); err != nil {
				log.Printf("metrics spool cleanup: %v", err)
			}
			<-ticker.C
		}
	}()
	return nil
}
