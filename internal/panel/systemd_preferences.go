package panel

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

type systemdPreference struct {
	Favorite   bool   `json:"favorite"`
	Membership string `json:"membership"`
	Category   string `json:"category"`
	Group      string `json:"group"`
}

func (m *nodeManager) systemdPreferences(w http.ResponseWriter, r *http.Request, node string) {
	if r.Method == http.MethodGet {
		rows, err := m.db.Query("SELECT unit,data FROM systemd_preferences WHERE node_id=?", node)
		if err != nil {
			fail(w, 500, err)
			return
		}
		defer rows.Close()
		result := map[string]systemdPreference{}
		for rows.Next() {
			var name, data string
			var p systemdPreference
			if err = rows.Scan(&name, &data); err != nil {
				fail(w, 500, err)
				return
			}
			if err = json.Unmarshal([]byte(data), &p); err != nil {
				fail(w, 500, err)
				return
			}
			result[name] = p
		}
		if err = rows.Err(); err != nil {
			fail(w, 500, err)
			return
		}
		respond(w, map[string]any{"units": result})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var b struct {
		Unit       string  `json:"unit"`
		Favorite   *bool   `json:"favorite"`
		Membership *string `json:"membership"`
		Category   *string `json:"category"`
		Group      *string `json:"group"`
	}
	if err := decode(w, r, &b); err != nil {
		fail(w, 400, err)
		return
	}
	if !systemdUnitName.MatchString(b.Unit) || len(b.Unit) > 255 {
		fail(w, 400, fmt.Errorf("单元名称无效"))
		return
	}
	if b.Membership != nil && *b.Membership != "auto" && *b.Membership != "my" && *b.Membership != "system" {
		fail(w, 400, fmt.Errorf("服务归属无效"))
		return
	}

	if b.Group != nil {
		v := strings.TrimSpace(*b.Group)
		b.Group = &v
		if utf8.RuneCountInString(v) > 64 || strings.ContainsAny(v, "\x00\r\n") {
			fail(w, 400, fmt.Errorf("分组名称无效"))
			return
		}
	}
	// Serialize read/merge/write so independent star and group changes cannot
	// silently overwrite each other, including requests from different browsers.
	m.mu.Lock()
	defer m.mu.Unlock()
	var data string
	p := systemdPreference{Membership: "auto"}
	err := m.db.QueryRow("SELECT data FROM systemd_preferences WHERE node_id=? AND unit=?", node, b.Unit).Scan(&data)
	if err != nil && err != sql.ErrNoRows {
		fail(w, 500, err)
		return
	}
	if err == nil {
		if err = json.Unmarshal([]byte(data), &p); err != nil {
			fail(w, 500, err)
			return
		}
	}
	if b.Favorite != nil {
		p.Favorite = *b.Favorite
	}
	if b.Membership != nil {
		p.Membership = *b.Membership
	}
	if b.Category != nil {
		p.Category = *b.Category
	}
	if b.Group != nil {
		p.Group = *b.Group
	}
	raw, _ := json.Marshal(p)
	if !p.Favorite && p.Category == "" && p.Group == "" && p.Membership == "auto" {
		_, err = m.db.Exec("DELETE FROM systemd_preferences WHERE node_id=? AND unit=?", node, b.Unit)
	} else {
		_, err = m.db.Exec("INSERT INTO systemd_preferences(node_id,unit,data) VALUES(?,?,?) ON CONFLICT(node_id,unit) DO UPDATE SET data=excluded.data", node, b.Unit, string(raw))
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, map[string]any{"unit": b.Unit, "preference": p})
}
