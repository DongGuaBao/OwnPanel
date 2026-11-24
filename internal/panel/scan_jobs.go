package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"
)

func (m *nodeManager) startScanJobs() error {
	// Scans only read files, so interrupted scans can safely be retried.
	if _, err := m.db.Exec("UPDATE scan_jobs SET status='queued' WHERE status='running'"); err != nil {
		return err
	}
	go func() {
		for {
			if err := m.runScanJob(); err != nil {
				log.Printf("scan queue: %v", err)
			}
			time.Sleep(time.Second)
		}
	}()
	return nil
}

func (m *nodeManager) runScanJob() error {
	var id, nodeID, path string
	err := m.db.QueryRow("UPDATE scan_jobs SET status='running' WHERE id=(SELECT id FROM scan_jobs WHERE status='queued' ORDER BY created,id LIMIT 1) RETURNING id,node_id,path").Scan(&id, &nodeID, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	result, err := m.executeScan(nodeID, path)
	status, message := "done", ""
	if err != nil {
		status, message = "failed", err.Error()
	}
	_, err = m.db.Exec("UPDATE scan_jobs SET status=?,result=?,error=? WHERE id=?", status, result, message, id)
	return err
}

func (m *nodeManager) executeScan(nodeID, path string) (string, error) {
	node, err := m.get(nodeID)
	if err != nil {
		return "", err
	}
	driver, err := m.driver(node)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/files/scan?path="+url.QueryEscape(path), nil)
	if err != nil {
		return "", err
	}
	response := httptest.NewRecorder()
	driver.ServeHTTP(response, req)
	if response.Code != 200 || !json.Valid(response.Body.Bytes()) {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return "", fmt.Errorf("扫描失败 (HTTP %d) %s", response.Code, body.Error)
	}
	return response.Body.String(), nil
}

func (m *nodeManager) handleScanJobs(w http.ResponseWriter, r *http.Request) bool {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	if len(parts) < 3 || len(parts) > 4 || parts[0] != "nodes" || parts[2] != "scan-jobs" {
		return false
	}
	nodeID := parts[1]
	if _, err := m.get(nodeID); err != nil {
		fail(w, 404, fmt.Errorf("主机不存在"))
		return true
	}
	switch {
	case len(parts) == 3 && r.Method == http.MethodPost:
		var body struct {
			Path string `json:"path"`
		}
		if err := decode(w, r, &body); err != nil {
			fail(w, 400, err)
			return true
		}
		if _, err := relativeRemote(body.Path); err != nil {
			fail(w, 400, err)
			return true
		}
		id := token()[:24]
		result, err := m.db.ExecContext(r.Context(), "INSERT INTO scan_jobs(id,node_id,path,status,created) SELECT ?,?,?,'queued',? WHERE (SELECT count(*) FROM scan_jobs WHERE status IN ('queued','running')) < 64", id, nodeID, body.Path, time.Now().UnixMilli())
		if err != nil {
			fail(w, 500, err)
			return true
		}
		count, _ := result.RowsAffected()
		if count == 0 {
			fail(w, 429, fmt.Errorf("扫描队列已满"))
			return true
		}
		m.record(nodeID, "file_scan_queued", map[string]string{"job": id})
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		respond(w, map[string]string{"id": id, "status": "queued"})
	case r.Method == http.MethodGet:
		query := "SELECT id,path,status,created,result,error FROM scan_jobs WHERE node_id=?"
		args := []any{nodeID}
		if len(parts) == 4 {
			query += " AND id=?"
			args = append(args, parts[3])
		}
		rows, err := m.db.QueryContext(r.Context(), query+" ORDER BY created DESC LIMIT 30", args...)
		if err != nil {
			fail(w, 500, err)
			return true
		}
		defer rows.Close()
		jobs := []map[string]any{}
		for rows.Next() {
			var id, path, status, result, message string
			var created int64
			if err = rows.Scan(&id, &path, &status, &created, &result, &message); err != nil {
				fail(w, 500, err)
				return true
			}
			item := map[string]any{"id": id, "path": path, "status": status, "created": created, "error": message}
			if result != "" {
				item["result"] = json.RawMessage(result)
			}
			jobs = append(jobs, item)
		}
		if err = rows.Err(); err != nil {
			fail(w, 500, err)
			return true
		}
		if len(parts) == 4 {
			if len(jobs) == 0 {
				fail(w, 404, fmt.Errorf("扫描任务不存在"))
			} else {
				respond(w, jobs[0])
			}
		} else {
			respond(w, map[string]any{"jobs": jobs})
		}
	default:
		fail(w, 405, fmt.Errorf("请求方式无效"))
	}
	return true
}
