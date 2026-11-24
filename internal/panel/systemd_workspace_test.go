package panel

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSystemdLocalClassification(t *testing.T) {
	for p, want := range map[string]string{"/etc/systemd/system/app.service": "local", "/usr/local/lib/systemd/system/mine.service": "local", "/opt/myapp/app.service": "local", "/usr/lib/systemd/system/ssh.service": "vendor", "/lib/systemd/system/cron.service": "vendor", "/run/systemd/generator/legacy.service": "generated", "/run/systemd/transient/job.service": "transient", "": "unknown", "/dev/null": "masked"} {
		if got := systemdOrigin(p); got != want {
			t.Errorf("%s: %s != %s", p, got, want)
		}
	}
}
func TestSystemdPreferencesPersistenceAndIsolation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "preferences.db")
	db, e := sql.Open("sqlite", file)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec("CREATE TABLE systemd_preferences(node_id TEXT,unit TEXT,data TEXT,PRIMARY KEY(node_id,unit))")
	if e != nil {
		t.Fatal(e)
	}
	m := &nodeManager{db: db}
	post := func(node, body string, intended int) {
		t.Helper()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		m.systemdPreferences(w, r, node)
		if w.Code != intended {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	post("node-a", `{"unit":"app.service","favorite":true,"group":"网站","membership":"my"}`, 200)
	post("node-a", `{"unit":"app.service","favorite":false}`, 200)
	post("node-b", `{"unit":"app.service","favorite":true,"group":"任务"}`, 200)
	post("node-a", `{"unit":"../../etc/passwd","favorite":true}`, 400)
	post("node-a", `{"unit":"app.service","membership":"invalid"}`, 400)
	db.Close()
	db, e = sql.Open("sqlite", file)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	m.db = db
	for node, want := range map[string]systemdPreference{"node-a": {Favorite: false, Group: "网站", Membership: "my"}, "node-b": {Favorite: true, Group: "任务", Membership: "auto"}} {
		w := httptest.NewRecorder()
		m.systemdPreferences(w, httptest.NewRequest("GET", "/", nil), node)
		var body struct {
			Units map[string]systemdPreference `json:"units"`
		}
		if e = json.Unmarshal(w.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if body.Units["app.service"] != want {
			t.Fatal(node, body.Units)
		}
	}
}

func TestSystemdStateReadsOnlySelectedUnit(t *testing.T) {
	calls := 0
	w := httptest.NewRecorder()
	serveSystemd(w, httptest.NewRequest("GET", "/api/systemd/unit?view=state&unit=demo.service", nil), func(_ context.Context, privileged bool, script string, args ...string) (string, error) {
		calls++
		if privileged || !strings.Contains(script, "'show'") || !strings.Contains(script, "'demo.service'") {
			t.Fatalf("unexpected status command %q", script)
		}
		return "ActiveState=active\nSubState=running\n", nil
	})
	var result struct{ Properties map[string]string }
	if calls != 1 || w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Properties["ActiveState"] != "active" {
		t.Fatal(calls, w.Code, w.Body.String())
	}
}

func TestSystemdActivitySkipsInventoryEnrichment(t *testing.T) {
	w := httptest.NewRecorder()
	calls := 0
	serveSystemd(w, httptest.NewRequest("GET", "/api/systemd?view=activity", nil), func(_ context.Context, privileged bool, script string, args ...string) (string, error) {
		calls++
		if privileged || !strings.Contains(script, "'list-units'") {
			t.Fatal(script)
		}
		return `[{"unit":"demo.service","active":"active","sub":"running","load":"loaded"}]`, nil
	})
	var result struct {
		Activity []struct{ Unit, Active string }
	}
	if calls != 1 || w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Activity) != 1 || result.Activity[0].Unit != "demo.service" || result.Activity[0].Active != "active" {
		t.Fatal(calls, w.Code, w.Body.String())
	}
}
