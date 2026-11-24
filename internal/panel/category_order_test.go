package panel

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCategoryOrderPersistsAndRejectsStaleLists(t *testing.T) {
	for _, kind := range []string{"nginx", "systemd"} {
		t.Run(kind, func(t *testing.T) {
			m := categoryManager(t)
			request := func(node, body string) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				method := "GET"
				if body != "" {
					method = "POST"
				}
				r := httptest.NewRequest(method, "/", strings.NewReader(body))
				if kind == "nginx" {
					m.nginxCategories(w, r, node)
				} else {
					m.systemdCategories(w, r, node)
				}
				return w
			}
			for _, node := range []string{"local", "ssh"} {
				for _, name := range []string{"A", "B", "C"} {
					if w := request(node, `{"action":"create","name":"`+name+`"}`); w.Code != 200 {
						t.Fatal(w.Body.String())
					}
				}
			}
			field := "keys"
			if kind == "systemd" {
				field = "categories"
			}
			for _, order := range []string{`["B","B","A"]`, `["B","A"]`, `["B","A","D"]`} {
				if w := request("local", `{"action":"reorder","`+field+`":`+order+`}`); w.Code != 400 {
					t.Fatalf("invalid order accepted: %s", w.Body.String())
				}
			}
			if w := request("local", `{"action":"reorder","`+field+`":["C","A","B"]}`); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			for node, expected := range map[string]string{"local": "CAB", "ssh": "ABC"} {
				var state struct {
					Categories []string `json:"categories"`
				}
				w := request(node, "")
				if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
					t.Fatal(err)
				}
				if strings.Join(state.Categories, "") != expected {
					t.Fatalf("%s: %v", node, state.Categories)
				}
			}
		})
	}
}
