package panel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func (m *nodeManager) systemdCategories(w http.ResponseWriter, r *http.Request, node string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var saved string
	err := m.db.QueryRow("SELECT data FROM systemd_categories WHERE node_id=?", node).Scan(&saved)
	if err != nil && err != sql.ErrNoRows {
		fail(w, 500, err)
		return
	}
	if r.Method == "GET" {
		var names []string
		if saved != "" {
			if err = json.Unmarshal([]byte(saved), &names); err != nil {
				fail(w, 500, err)
				return
			}
		}
		respond(w, map[string]any{"categories": names, "initialized": saved != ""})
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var b struct {
		Action     string            `json:"action"`
		Name       string            `json:"name"`
		Target     string            `json:"target"`
		Categories []string          `json:"categories"`
		Initial    bool              `json:"initial"`
		Units      map[string]string `json:"units"`
	}
	if err = decode(w, r, &b); err != nil {
		fail(w, 400, err)
		return
	}
	if b.Action != "" {
		m.mutateSystemdCategory(w, r, node, saved, b.Action, b.Name, b.Target, b.Categories)
		return
	}
	if b.Initial && saved != "" {
		respond(w, map[string]bool{"ok": true})
		return
	}
	seen := map[string]bool{}
	for _, name := range b.Categories {
		if strings.TrimSpace(name) == "" || len(name) > 256 || seen[name] {
			fail(w, 400, fmt.Errorf("分类名称无效或重复"))
			return
		}
		seen[name] = true
	}
	tx, err := m.db.Begin()
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer tx.Rollback()
	raw, _ := json.Marshal(b.Categories)
	if _, err = tx.Exec("INSERT INTO systemd_categories(node_id,data) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET data=excluded.data", node, string(raw)); err != nil {
		fail(w, 500, err)
		return
	}
	if b.Initial {
		for unit, category := range b.Units {
			if !systemdUnitName.MatchString(unit) || !seen[category] {
				fail(w, 400, fmt.Errorf("初始分类无效"))
				return
			}
			var data string
			p := systemdPreference{}
			err = tx.QueryRow("SELECT data FROM systemd_preferences WHERE node_id=? AND unit=?", node, unit).Scan(&data)
			if err != nil && err != sql.ErrNoRows {
				fail(w, 500, err)
				return
			}
			if data != "" {
				if err = json.Unmarshal([]byte(data), &p); err != nil {
					fail(w, 500, err)
					return
				}
			}
			p.Category = category
			v, _ := json.Marshal(p)
			if _, err = tx.Exec("INSERT INTO systemd_preferences(node_id,unit,data) VALUES(?,?,?) ON CONFLICT(node_id,unit) DO UPDATE SET data=excluded.data", node, unit, string(v)); err != nil {
				fail(w, 500, err)
				return
			}
		}
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, map[string]bool{"ok": true})
}

// Called with m.mu held. The list and every affected service preference commit
// together, and the browser receives one consistent result without follow-up writes.
func (m *nodeManager) mutateSystemdCategory(w http.ResponseWriter, r *http.Request, node, saved, action, name, target string, order []string) {
	names := []string{}
	if saved != "" {
		if err := json.Unmarshal([]byte(saved), &names); err != nil {
			fail(w, 500, err)
			return
		}
	}
	valid := func(value string) bool {
		return value != "" && value == strings.TrimSpace(value) && len(value) <= 256 && !strings.ContainsAny(value, "\x00\r\n")
	}
	index := -1
	for i, value := range names {
		if value == name {
			index = i
		}
	}
	switch action {
	case "reorder":
		if !sameCategoryNames(names, order) {
			fail(w, 400, fmt.Errorf("分类已变化，请刷新后重新排序"))
			return
		}
		names = order
	case "create":
		if !valid(name) || index >= 0 {
			fail(w, 400, fmt.Errorf("分类名称无效或已存在"))
			return
		}
		names = append(names, name)
	case "rename":
		if index < 0 || !valid(target) {
			fail(w, 400, fmt.Errorf("分类不存在或新名称无效"))
			return
		}
		for _, value := range names {
			if value == target {
				fail(w, 400, fmt.Errorf("分类名称已存在"))
				return
			}
		}
		names[index] = target
	case "delete":
		if index < 0 {
			fail(w, 400, fmt.Errorf("分类不存在"))
			return
		}
		names = append(names[:index], names[index+1:]...)
	default:
		fail(w, 400, fmt.Errorf("分类操作无效"))
		return
	}
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(r.Context(), "SELECT unit,data FROM systemd_preferences WHERE node_id=?", node)
	if err != nil {
		fail(w, 500, err)
		return
	}
	preferences := map[string]systemdPreference{}
	changed := map[string]systemdPreference{}
	for rows.Next() {
		var unit, data string
		var preference systemdPreference
		if err = rows.Scan(&unit, &data); err == nil {
			err = json.Unmarshal([]byte(data), &preference)
		}
		if err != nil {
			rows.Close()
			fail(w, 500, err)
			return
		}
		if (action == "rename" || action == "delete") && preference.Category == name {
			preference.Category = ""
			if action == "rename" {
				preference.Category = target
			}
			changed[unit] = preference
		}
		preferences[unit] = preference
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, err)
		return
	}
	for unit, preference := range changed {
		data, _ := json.Marshal(preference)
		if _, err = tx.ExecContext(r.Context(), "UPDATE systemd_preferences SET data=? WHERE node_id=? AND unit=?", string(data), node, unit); err != nil {
			fail(w, 500, err)
			return
		}
	}
	data, _ := json.Marshal(names)
	if _, err = tx.ExecContext(r.Context(), "INSERT INTO systemd_categories(node_id,data) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET data=excluded.data", node, string(data)); err != nil {
		fail(w, 500, err)
		return
	}
	if err = tx.Commit(); err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, map[string]any{"categories": names, "units": preferences})
}
