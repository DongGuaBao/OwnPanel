package panel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"
)

type nginxCategoryState struct {
	Categories  []string          `json:"categories"`
	Assignments map[string]string `json:"assignments"`
}

// Category mutations merge against the saved state under the same lock, so a
// rename/delete and all affected assignments are committed together.
func (m *nodeManager) nginxCategories(w http.ResponseWriter, r *http.Request, node string) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Action string   `json:"action"`
		Name   string   `json:"name"`
		Target string   `json:"target"`
		Keys   []string `json:"keys"`
	}
	if r.Method == http.MethodPost {
		if err := decode(w, r, &body); err != nil {
			fail(w, 400, err)
			return
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := nginxCategoryState{Categories: []string{}, Assignments: map[string]string{}}
	var saved string
	err := m.db.QueryRowContext(r.Context(), "SELECT data FROM nginx_categories WHERE node_id=?", node).Scan(&saved)
	if err != nil && err != sql.ErrNoRows {
		fail(w, 500, err)
		return
	}
	if saved != "" {
		if err = json.Unmarshal([]byte(saved), &state); err != nil {
			fail(w, 500, err)
			return
		}
	}
	if r.Method == http.MethodGet {
		respond(w, state)
		return
	}
	validName := func(name string) bool {
		return name != "" && name == strings.TrimSpace(name) && utf8.RuneCountInString(name) <= 64 && !strings.ContainsAny(name, "\x00\r\n")
	}
	invalid := func(message string) { fail(w, 400, fmt.Errorf("%s", message)) }
	index := slices.Index(state.Categories, body.Name)
	switch body.Action {
	case "reorder":
		if !sameCategoryNames(state.Categories, body.Keys) {
			invalid("分类已变化，请刷新后重新排序")
			return
		}
		state.Categories = body.Keys
	case "create":
		if !validName(body.Name) || index >= 0 || len(state.Categories) >= 128 {
			invalid("分类名称无效、重复或分类数量已达上限")
			return
		}
		state.Categories = append(state.Categories, body.Name)
	case "rename":
		if index < 0 || !validName(body.Target) || slices.Contains(state.Categories, body.Target) {
			invalid("原分类不存在或新名称无效、重复")
			return
		}
		state.Categories[index] = body.Target
		for key, name := range state.Assignments {
			if name == body.Name {
				state.Assignments[key] = body.Target
			}
		}
	case "delete":
		if index < 0 {
			invalid("分类不存在")
			return
		}
		state.Categories = slices.Delete(state.Categories, index, index+1)
		for key, name := range state.Assignments {
			if name == body.Name {
				delete(state.Assignments, key)
			}
		}
	case "assign":
		if len(body.Keys) == 0 || len(body.Keys) > 1000 || (body.Target != "" && !slices.Contains(state.Categories, body.Target)) {
			invalid("请选择站点及有效分类（单次最多 1000 项）")
			return
		}
		for _, key := range body.Keys {
			if len(key) > 8192 || strings.ContainsAny(key, "\x00\r\n") || (!strings.HasPrefix(key, "managed:") && !strings.HasPrefix(key, "external:")) || strings.HasSuffix(key, ":") {
				invalid("站点标识无效")
				return
			}
		}
		for _, key := range body.Keys {
			if body.Target == "" {
				delete(state.Assignments, key)
			} else {
				state.Assignments[key] = body.Target
			}
		}
	default:
		invalid("不支持的分类操作")
		return
	}
	raw, err := json.Marshal(state)
	if err == nil {
		_, err = m.db.ExecContext(r.Context(), "INSERT INTO nginx_categories(node_id,data) VALUES(?,?) ON CONFLICT(node_id) DO UPDATE SET data=excluded.data", node, string(raw))
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, state)
}

func sameCategoryNames(current, order []string) bool {
	if len(current) != len(order) {
		return false
	}
	seen := map[string]bool{}
	for _, name := range current {
		seen[name] = true
	}
	for _, name := range order {
		if !seen[name] {
			return false
		}
		delete(seen, name)
	}
	return len(seen) == 0
}
