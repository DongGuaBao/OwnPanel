package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"
)

func metricTime(raw []byte) (int64, error) {
	var sample struct {
		Time int64 `json:"time"`
	}
	if err := json.Unmarshal(raw, &sample); err != nil {
		return 0, err
	}
	if sample.Time <= 0 || sample.Time > time.Now().Add(5*time.Minute).UnixMilli() {
		return 0, fmt.Errorf("invalid metric timestamp")
	}
	return sample.Time, nil
}

func (m *nodeManager) collectMetrics(ctx context.Context, node Node) error {
	driver, err := m.driver(node)
	if err != nil {
		return err
	}
	if node.Mode == "agent" {
		var cursor int64
		if err := m.db.QueryRowContext(ctx, "SELECT coalesce(max(sampled_at),0) FROM metrics_history WHERE node_id=?", node.ID).Scan(&cursor); err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/metrics/spool?after="+strconv.FormatInt(cursor, 10), nil)
		if err != nil {
			return err
		}
		response := httptest.NewRecorder()
		driver.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			if response.Code != http.StatusOK {
				return fmt.Errorf("agent metrics spool returned HTTP %d", response.Code)
			}
			var batch struct {
				Samples []json.RawMessage `json:"samples"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &batch); err != nil {
				return err
			}
			if len(batch.Samples) > 180 {
				return fmt.Errorf("agent metrics batch too large")
			}
			tx, err := m.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			for _, sample := range batch.Samples {
				ts, err := metricTime(sample)
				if err != nil {
					return err
				}
				if ts <= cursor {
					return fmt.Errorf("agent metrics out of order")
				}
				cursor = ts
				if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO metrics_history SELECT ?,?,? WHERE EXISTS (SELECT 1 FROM nodes WHERE id=?)", node.ID, ts, string(sample), node.ID); err != nil {
					return err
				}
			}
			return tx.Commit()
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/api/metrics", nil)
	if err != nil {
		return err
	}
	response := httptest.NewRecorder()
	driver.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !json.Valid(response.Body.Bytes()) {
		return fmt.Errorf("invalid metrics response (HTTP %d)", response.Code)
	}
	_, err = m.db.ExecContext(ctx, "INSERT OR IGNORE INTO metrics_history SELECT ?,?,? WHERE EXISTS (SELECT 1 FROM nodes WHERE id=?)", node.ID, time.Now().UnixMilli(), response.Body.String(), node.ID)
	return err
}

func (m *nodeManager) serveMetricsHistory(w http.ResponseWriter, r *http.Request, id string) {
	if _, err := m.get(id); err != nil {
		fail(w, http.StatusNotFound, fmt.Errorf("主机不存在"))
		return
	}
	query := "SELECT data FROM (SELECT data,sampled_at FROM metrics_history WHERE node_id=? ORDER BY sampled_at DESC LIMIT 90) ORDER BY sampled_at"
	args := []any{id}
	switch r.URL.Query().Get("range") {
	case "", "recent":
	case "day", "week":
		duration := 24 * time.Hour
		if r.URL.Query().Get("range") == "week" {
			duration *= 7
		}
		const buckets = 120
		end := time.Now()
		start := end.Add(-duration)
		query = `SELECT h.data FROM metrics_history h JOIN (
			SELECT MAX(sampled_at) AS timestamp FROM metrics_history
			WHERE node_id=? AND sampled_at>? AND sampled_at<=?
			GROUP BY (sampled_at-?-1)/?
		) samples ON h.sampled_at=samples.timestamp WHERE h.node_id=? ORDER BY h.sampled_at`
		args = []any{id, start.UnixMilli(), end.UnixMilli(), start.UnixMilli(), duration.Milliseconds() / buckets, id}
	default:
		fail(w, http.StatusBadRequest, fmt.Errorf("监控历史时间范围无效"))
		return
	}
	rows, err := m.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	defer rows.Close()
	samples := []json.RawMessage{}
	for rows.Next() {
		var data string
		if err = rows.Scan(&data); err != nil {
			break
		}
		samples = append(samples, json.RawMessage(data))
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	respond(w, map[string]any{"samples": samples})
}
