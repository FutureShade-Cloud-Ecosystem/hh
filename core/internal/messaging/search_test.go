package messaging

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestRouteSearch_QueryValidationAndEmptyScopes(t *testing.T) {
	fs := newFakeStore()
	mux, bearer := newSavedUnitRoutes(t, fs, s5UnitCallerID)
	for _, target := range []string{
		"/api/v1/messaging/search",
		"/api/v1/messaging/search?q=",
		"/api/v1/messaging/search?q=a",
		"/api/v1/messaging/search?q=++a++",
	} {
		rec := savedUnitDo(t, mux, bearer, http.MethodGet, target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: %d %s, want 400", target, rec.Code, rec.Body.String())
		}
	}
	rec := savedUnitDo(t, mux, bearer, http.MethodGet, "/api/v1/messaging/search?q=ab", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid search: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
		People   []map[string]any `json:"people"`
		Channels []map[string]any `json:"channels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search: %v (%s)", err, rec.Body.String())
	}
	if body.Messages == nil || body.People == nil || body.Channels == nil {
		t.Fatalf("empty scopes must be arrays: %s", rec.Body.String())
	}
}
