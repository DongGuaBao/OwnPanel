package panel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func categoryManager(t *testing.T) *nodeManager {
	t.Helper()
	old := cfg
	cfg.data = t.TempDir()
	cfg.socket = ""
	cfg.agentHosts = "localhost"
	m, err := newNodeManager()
	if err != nil {
		cfg = old
		t.Fatal(err)
	}
	t.Cleanup(func() { m.db.Close(); cfg = old })
	return m
}

func categoryRequest(m *nodeManager, node, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	method := "GET"
	if body != "" {
		method = "POST"
	}
	m.nginxCategories(w, httptest.NewRequest(method, "/", strings.NewReader(body)), node)
	return w
}

func readCategories(t *testing.T, m *nodeManager, node string) nginxCategoryState {
	t.Helper()
	w := categoryRequest(m, node, "")
	var state nginxCategoryState
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil || w.Code != 200 {
		t.Fatalf("read: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	return state
}

func TestNginxCategoriesPersistenceIsolationAndAtomicMutations(t *testing.T) {
	m := categoryManager(t)
	post := func(node, body string, code int) {
		t.Helper()
		w := categoryRequest(m, node, body)
		if w.Code != code {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	post("agent", `{"action":"create","name":"业务网站"}`, 200)
	post("ssh", `{"action":"create","name":"业务网站"}`, 200)
	post("agent", `{"action":"assign","target":"业务网站","keys":["managed:one","external:existing"]}`, 200)
	post("ssh", `{"action":"assign","target":"业务网站","keys":["managed:one"]}`, 200)
	// A bad key must reject the whole batch, preserving earlier assignments.
	post("agent", `{"action":"assign","target":"","keys":["managed:one","invalid"]}`, 400)
	post("agent", `{"action":"rename","name":"业务网站","target":"生产服务"}`, 200)
	if state := readCategories(t, m, "agent"); state.Assignments["managed:one"] != "生产服务" || state.Assignments["external:existing"] != "生产服务" {
		t.Fatal(state)
	}
	// Reopen SQLite to prove this is not browser/session state.
	m.db.Close()
	var err error
	m.db, err = sql.Open("sqlite", filepath.Join(cfg.data, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	if got := readCategories(t, m, "agent").Assignments["external:existing"]; got != "生产服务" {
		t.Fatal(got)
	}
	post("agent", `{"action":"delete","name":"生产服务"}`, 200)
	state := readCategories(t, m, "agent")
	if len(state.Categories) != 0 || len(state.Assignments) != 0 {
		t.Fatal(state)
	}
	if got := readCategories(t, m, "ssh").Assignments["managed:one"]; got != "业务网站" {
		t.Fatal("cross-host mutation:", got)
	}
}

func TestNginxCategoriesValidation(t *testing.T) {
	m := categoryManager(t)
	for _, body := range []string{
		`{"action":"create","name":""}`,
		`{"action":"create","name":" leading"}`,
		`{"action":"create","name":"bad\nname"}`,
		`{"action":"create","name":"` + strings.Repeat("站", 65) + `"}`,
		`{"action":"assign","target":"missing","keys":["managed:a"]}`,
		`{"action":"assign","keys":["managed:"]}`,
		`{"action":"assign","keys":[]}`,
		`{"action":"delete","name":"missing"}`,
		`{"action":"rename","name":"missing","target":"valid"}`,
		`{"action":"unknown"}`,
	} {
		if w := categoryRequest(m, "agent", body); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	categoryRequest(m, "agent", `{"action":"create","name":"one"}`)
	categoryRequest(m, "agent", `{"action":"create","name":"two"}`)
	for _, body := range []string{`{"action":"create","name":"one"}`, `{"action":"rename","name":"one","target":"two"}`} {
		if w := categoryRequest(m, "agent", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	m.nginxCategories(w, httptest.NewRequest("DELETE", "/", nil), "agent")
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestNginxCategoriesConcurrentAssignments(t *testing.T) {
	m := categoryManager(t)
	categoryRequest(m, "agent", `{"action":"create","name":"服务"}`)
	var group sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 24)
	for i := 0; i < cap(results); i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			results <- categoryRequest(m, "agent", fmt.Sprintf(`{"action":"assign","target":"服务","keys":["managed:site-%d"]}`, i))
		}(i)
	}
	group.Wait()
	close(results)
	for w := range results {
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if got := len(readCategories(t, m, "agent").Assignments); got != cap(results) {
		t.Fatalf("lost concurrent updates: %d", got)
	}
}
