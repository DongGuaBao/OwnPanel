package panel

import (
	"fmt"
	"net/http"
)

func (m *nodeManager) nginxNotes(w http.ResponseWriter, r *http.Request, node string) {
	ctx := r.Context()
	if r.Method == http.MethodGet {
		rows, err := m.db.QueryContext(ctx, "SELECT site_key,note FROM nginx_notes WHERE node_id=?", node)
		if err != nil {
			fail(w, 500, err)
			return
		}
		defer rows.Close()
		notes := map[string]string{}
		for rows.Next() {
			var key, note string
			if err := rows.Scan(&key, &note); err != nil {
				fail(w, 500, err)
				return
			}
			notes[key] = note
		}
		if err := rows.Err(); err != nil {
			fail(w, 500, err)
			return
		}
		respond(w, notes)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Key  string `json:"key"`
		Note string `json:"note"`
	}
	if err := decode(w, r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if len(body.Key) == 0 || len(body.Key) > 8192 || len(body.Note) > 16384 {
		fail(w, 400, fmt.Errorf("站点标识无效或备注超过 16 KiB"))
		return
	}
	var err error
	if body.Note == "" {
		_, err = m.db.ExecContext(ctx, "DELETE FROM nginx_notes WHERE node_id=? AND site_key=?", node, body.Key)
	} else {
		_, err = m.db.ExecContext(ctx, "INSERT INTO nginx_notes(node_id,site_key,note) VALUES(?,?,?) ON CONFLICT(node_id,site_key) DO UPDATE SET note=excluded.note", node, body.Key, body.Note)
	}
	if err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, map[string]bool{"ok": true})
}
