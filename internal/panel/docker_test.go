package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestDockerResourceOperations(t *testing.T) {
	tests := []struct {
		method, path, body string
		code               int
		args               []string
	}{
		{"POST", "containers/create", `{"name":"frontend","image":"nginx:stable","network":"apps","restart":"unless-stopped","ports":[{"address":"::1","host":8080,"container":80,"protocol":"tcp"}],"volumes":[{"name":"web","path":"/data","readOnly":true}]}`, 200, []string{"container", "run", "--detach", "--pull=never", "--name", "frontend", "--restart", "unless-stopped", "--network", "apps", "--publish", "[::1]:8080:80/tcp", "--mount", "type=volume,source=web,target=/data,readonly", "nginx:stable"}},
		{"POST", "images/remove", `{"target":"registry.internal:5000/app:v1"}`, 200, []string{"image", "rm", "registry.internal:5000/app:v1"}},
		{"POST", "volumes/remove", `{"target":"data1"}`, 200, []string{"volume", "rm", "data1"}},
		{"POST", "containers/remove", `{"target":"--force"}`, 400, nil},
		{"POST", "containers/create", `{"name":"app","image":"alpine","volumes":[{"name":"/","path":"/host"}]}`, 400, nil},
		{"POST", "containers/create", `{"name":"app","image":"alpine","ports":[{"address":"","host":80,"container":80,"protocol":"tcp"}]}`, 400, nil},
		{"POST", "images/pull", `{"target":"--platform"}`, 400, nil},
		{"GET", "containers/stop", "", 405, nil},
		{"POST", "volumes/prune", `{}`, 400, nil},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			var called []string
			rec := httptest.NewRecorder()
			serveDocker(rec, httptest.NewRequest(tc.method, "/api/docker/"+tc.path+"?engine=test", strings.NewReader(tc.body)), []dockerEngine{{"test", "Test"}}, func(id string) dockerRunner {
				return func(_ context.Context, args ...string) (string, error) { called = args; return "", nil }
			})
			if rec.Code != tc.code || !reflect.DeepEqual(called, tc.args) {
				t.Fatalf("status %d args %v response %s", rec.Code, called, rec.Body.String())
			}
		})
	}
}
func TestDockerEngineSelectionAndErrors(t *testing.T) {
	for _, tc := range []struct {
		path, method, body string
		status             int
	}{
		{"containers?engine=unknown", "GET", "", 400}, {"engines", "GET", "", 200},
		{"containers?engine=test", "GET", "", 502}, {"images/pull?engine=test", "POST", `{"target":"alpine"}`, 200},
	} {
		rec := httptest.NewRecorder()
		serveDocker(rec, httptest.NewRequest(tc.method, "/api/docker/"+tc.path, strings.NewReader(tc.body)), []dockerEngine{{"test", "Test"}}, func(id string) dockerRunner {
			if id != "test" {
				t.Fatal("wrong engine")
			}
			return func(_ context.Context, args ...string) (string, error) {
				return "", errors.New("daemon unavailable")
			}
		})
		var data map[string]any
		if rec.Code != tc.status || json.Unmarshal(rec.Body.Bytes(), &data) != nil {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
		}
		if tc.path != "engines" && data["error"] == nil {
			t.Fatalf("%s omitted error", tc.path)
		}
	}
}

func TestDockerContainerInventory(t *testing.T) {
	rec := httptest.NewRecorder()
	serveDocker(rec, httptest.NewRequest(http.MethodGet, "/api/docker/containers?engine=login", nil), []dockerEngine{{"login", "SSH"}}, func(string) dockerRunner {
		return func(_ context.Context, args ...string) (string, error) {
			if args[0] != "container" || args[1] != "ls" || !strings.Contains(args[len(args)-1], "com.docker.compose.project") {
				t.Fatal(args)
			}
			return "{\"id\":\"a\",\"name\":\"web\",\"project\":\"app\"}\n{\"id\":\"b\",\"name\":\"db\"}\n", nil
		}
	})
	var result struct{ Rows []map[string]string }
	if json.Unmarshal(rec.Body.Bytes(), &result) != nil || len(result.Rows) != 2 || result.Rows[0]["project"] != "app" {
		t.Fatal(rec.Body.String())
	}
}
