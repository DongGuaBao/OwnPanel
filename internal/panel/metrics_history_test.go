package panel

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMetricsHistoryRanges(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.data = t.TempDir()
	cfg.socket = ""
	manager, err := newNodeManager()
	if err != nil {
		t.Fatal(err)
	}
	defer manager.db.Close()
	if _, err := manager.db.Exec("INSERT INTO nodes(id,data) VALUES ('test','{}'),('other','{}')"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for _, sample := range []struct {
		node, label string
		age         time.Duration
	}{
		{"test", "old", 48 * time.Hour}, {"test", "latest", time.Hour}, {"test", "expired", 8 * 24 * time.Hour}, {"other", "foreign", time.Hour},
	} {
		data, _ := json.Marshal(sample.label)
		if _, err := manager.db.Exec("INSERT INTO metrics_history VALUES (?,?,?)", sample.node, now.Add(-sample.age).UnixMilli(), string(data)); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		span string
		code int
		want string
	}{
		{"day", 200, "latest"}, {"week", 200, "old,latest"}, {"invalid", 400, ""},
	} {
		response := httptest.NewRecorder()
		manager.serveMetricsHistory(response, httptest.NewRequest("GET", "/api/nodes/test/metrics/history?range="+test.span, nil), "test")
		if response.Code != test.code {
			t.Fatalf("%s: HTTP %d: %s", test.span, response.Code, response.Body.String())
		}
		if test.code != 200 {
			continue
		}
		var payload struct {
			Samples []string `json:"samples"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		got := ""
		for _, label := range payload.Samples {
			if got != "" {
				got += ","
			}
			got += label
		}
		if got != test.want {
			t.Fatalf("%s: got %s want %s", test.span, got, test.want)
		}
	}
}
