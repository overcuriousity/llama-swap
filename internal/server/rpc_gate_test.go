package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mostlygeek/llama-swap/internal/config"
)

func TestServer_ListModelsHidesUnhealthyRPC(t *testing.T) {
	local := newStubRouter([]string{"healthy", "broken"}, "")
	local.unhealthyRPC = map[string]bool{"broken": true}

	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{
		"healthy": {},
		"broken":  {},
	}}

	w := httptest.NewRecorder()
	s.handleListModels(w, httptest.NewRequest("GET", "/v1/models", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	ids := map[string]bool{}
	for _, rec := range body.Data {
		ids[rec.ID] = true
	}
	if !ids["healthy"] {
		t.Error("healthy model should be listed")
	}
	if ids["broken"] {
		t.Error("model with unhealthy RPC endpoints should be filtered from /v1/models")
	}
}

func TestServer_DispatchRejectsUnhealthyRPC(t *testing.T) {
	local := newStubRouter([]string{"broken"}, "upstream response")
	local.unhealthyRPC = map[string]bool{"broken": true}

	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"broken": {}}}

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"broken"}`))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "RPC endpoints unhealthy") {
		t.Errorf("body = %q, want it to mention unhealthy RPC endpoints", w.Body.String())
	}
}

func TestServer_DispatchAllowsHealthyRPC(t *testing.T) {
	local := newStubRouter([]string{"fine"}, "upstream response")

	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"fine": {}}}

	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"fine"}`))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
}

// The /upstream passthrough dispatches through serveLocal as well, so it must
// not be a way around the RPC gate.
func TestServer_UpstreamRejectsUnhealthyRPC(t *testing.T) {
	local := newStubRouter([]string{"broken"}, "upstream response")
	local.unhealthyRPC = map[string]bool{"broken": true}

	s := newTestServer(local, newStubRouter(nil, ""))
	s.cfg = config.Config{Models: map[string]config.ModelConfig{"broken": {}}}

	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, httptest.NewRequest("GET", "/upstream/broken/props", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body.String())
	}
}
