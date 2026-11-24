package panel

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

// Opt in to read-only checks against a selected local Engine, including its CLI template version.
func TestDockerLocalInventories(t *testing.T) {
	engine := os.Getenv("OWNPANEL_TEST_DOCKER_ENGINE")
	if engine == "" {
		t.Skip("set OWNPANEL_TEST_DOCKER_ENGINE to a local engine ID")
	}
	mux := http.NewServeMux()
	registerDocker(mux)
	for resource := range dockerInventories {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/docker/"+resource+"?engine="+url.QueryEscape(engine), nil))
		var result struct{ Rows []map[string]string }
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &result) != nil || result.Rows == nil {
			t.Fatalf("%s: status %d; %.300s", resource, rec.Code, rec.Body.String())
		}
		t.Logf("%s: %d rows", resource, len(result.Rows))
	}
}
