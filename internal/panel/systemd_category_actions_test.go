package panel

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestSystemdCategoryAtomicActions(t *testing.T) {
	m := categoryManager(t)
	request := func(node, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		m.systemdCategories(w, httptest.NewRequest("POST", "/", strings.NewReader(body)), node)
		return w
	}
	for _, node := range []string{"local", "ssh"} {
		if w := request(node, `{"action":"create","name":"业务"}`); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		w := httptest.NewRecorder()
		m.systemdPreferences(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"unit":"app.service","category":"业务","favorite":true,"membership":"my","group":"项目"}`)), node)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	w := request("local", `{"action":"rename","name":"业务","target":"生产"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var state struct {
		Categories []string                     `json:"categories"`
		Units      map[string]systemdPreference `json:"units"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	preference := state.Units["app.service"]
	if len(state.Categories) != 1 || state.Categories[0] != "生产" || preference.Category != "生产" || !preference.Favorite || preference.Membership != "my" || preference.Group != "项目" {
		t.Fatal(state)
	}
	if w = request("local", `{"action":"rename","name":"生产","target":"bad\nname"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = request("local", `{"action":"delete","name":"生产"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = request("local", `{"action":"create","name":"生产"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = httptest.NewRecorder()
	m.systemdPreferences(w, httptest.NewRequest("GET", "/", nil), "local")
	if strings.Contains(w.Body.String(), `"category":"生产"`) {
		t.Fatal("deleted category resurrected an assignment")
	}
	w = httptest.NewRecorder()
	m.systemdPreferences(w, httptest.NewRequest("GET", "/", nil), "ssh")
	if !strings.Contains(w.Body.String(), `"category":"业务"`) {
		t.Fatal("cross-host mutation", w.Body.String())
	}
	// Corrupt stored preferences must roll back a rename, including category names.
	if _, err := m.db.Exec("UPDATE systemd_preferences SET data='invalid-json' WHERE node_id='local'"); err != nil {
		t.Fatal(err)
	}
	if w = request("local", `{"action":"rename","name":"生产","target":"不应保存"}`); w.Code != 500 {
		t.Fatal(w.Code)
	}
	var saved string
	if err := m.db.QueryRow("SELECT data FROM systemd_categories WHERE node_id='local'").Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if saved != `["生产"]` {
		t.Fatal("partial rename committed", saved)
	}
}
func TestSystemdConcurrentCategoryCreation(t *testing.T) {
	m := categoryManager(t)
	var wg sync.WaitGroup
	codes := make(chan int, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			m.systemdCategories(w, httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"action":"create","name":"分类%d"}`, i))), "local")
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal(code)
		}
	}
	var saved string
	if err := m.db.QueryRow("SELECT data FROM systemd_categories WHERE node_id='local'").Scan(&saved); err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := json.Unmarshal([]byte(saved), &names); err != nil || len(names) != 16 {
		t.Fatal(len(names), err)
	}
}
