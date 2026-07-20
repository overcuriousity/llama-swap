package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfigYAML = `models:
  model1:
    cmd: test-server --port ${PORT}
`

func newConfigEditorServer(t *testing.T, path string) *Server {
	t.Helper()
	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.SetConfigSource(ConfigSource{Path: path, Example: []byte("example: true\n")})
	return s
}

func TestServer_ConfigCurrentReturnsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(validConfigYAML), 0644); err != nil {
		t.Fatal(err)
	}

	s := newConfigEditorServer(t, path)
	w := httptest.NewRecorder()
	s.handleAPIConfigCurrent(w, httptest.NewRequest("GET", "/api/config/current", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != validConfigYAML {
		t.Errorf("body = %q, want %q", w.Body.String(), validConfigYAML)
	}
}

func TestServer_ConfigCurrentWithoutPathIs404(t *testing.T) {
	s := newConfigEditorServer(t, "")
	w := httptest.NewRecorder()
	s.handleAPIConfigCurrent(w, httptest.NewRequest("GET", "/api/config/current", nil))

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestServer_ConfigUpdateWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("models: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newConfigEditorServer(t, path)
	w := httptest.NewRecorder()
	s.handleAPIConfigUpdate(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(validConfigYAML)))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != validConfigYAML {
		t.Errorf("file = %q, want %q", got, validConfigYAML)
	}
}

// An invalid document must be rejected before it reaches disk: writing it would
// break the proxy on the next reload.
func TestServer_ConfigUpdateRejectsInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "models: {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}

	s := newConfigEditorServer(t, path)
	w := httptest.NewRecorder()
	s.handleAPIConfigUpdate(w, httptest.NewRequest("POST", "/api/config",
		strings.NewReader("models: [this is not: valid: yaml\n")))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("config file was modified despite invalid input: %q", got)
	}
}

func TestServer_ConfigUpdateRejectsOversizedBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("models: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := newConfigEditorServer(t, path)
	w := httptest.NewRecorder()
	body := strings.Repeat("a", maxConfigUploadBytes+1)
	s.handleAPIConfigUpdate(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(body)))

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", w.Code)
	}
}

func TestServer_ConfigExampleReturnsEmbedded(t *testing.T) {
	s := newConfigEditorServer(t, filepath.Join(t.TempDir(), "config.yaml"))
	w := httptest.NewRecorder()
	s.handleAPIConfigExample(w, httptest.NewRequest("GET", "/api/config/example", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "example: true\n" {
		t.Errorf("body = %q", w.Body.String())
	}
}
