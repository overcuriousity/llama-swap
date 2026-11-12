package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// A page on another site must not be able to read or replace the config:
// the write path runs whatever cmd the document names.
func TestServer_ConfigEditorRejectsCrossOrigin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "models: {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	s := newConfigEditorServer(t, path)

	cases := []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"cross-site fetch", map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"same-site fetch", map[string]string{"Sec-Fetch-Site": "same-site"}, http.StatusForbidden},
		{"foreign origin", map[string]string{"Origin": "http://evil.example"}, http.StatusForbidden},
		{"same-origin fetch", map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"matching origin", map[string]string{"Origin": "http://example.com"}, http.StatusOK},
		{"non-browser client", nil, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, req := range []*http.Request{
				httptest.NewRequest("GET", "/api/config/current", nil),
				httptest.NewRequest("POST", "/api/config", strings.NewReader(original)),
			} {
				for k, v := range tc.headers {
					req.Header.Set(k, v)
				}
				w := httptest.NewRecorder()
				s.handler.ServeHTTP(w, req)
				if w.Code != tc.want {
					t.Errorf("%s %s: status = %d, want %d; body: %s",
						req.Method, req.URL.Path, w.Code, tc.want, w.Body.String())
				}
			}
		})
	}
}

// With -config-dir the edited file is only half the config. A document that
// conflicts with the directory must be rejected here, not by the reload.
func TestServer_ConfigUpdateValidatesWithConfigDir(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	dir := filepath.Join(root, "conf.d")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	original := "models: {}\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "m.yaml"), []byte(validConfigYAML), 0644); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(newStubRouter(nil, ""), newStubRouter(nil, ""))
	s.SetConfigSource(ConfigSource{Path: path, Dir: dir})

	// model1 is already defined in conf.d/m.yaml.
	w := httptest.NewRecorder()
	s.handleAPIConfigUpdate(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(validConfigYAML)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body: %s", w.Code, w.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != original {
		t.Errorf("config file was modified despite the conflict: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(root, ".llama-swap-config-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// Saving must keep the file's permissions and write through a symlink rather
// than replacing it.
func TestServer_ConfigUpdateKeepsModeAndSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real.yaml")
	link := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(target, []byte("models: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	s := newConfigEditorServer(t, link)
	w := httptest.NewRecorder()
	s.handleAPIConfigUpdate(w, httptest.NewRequest("POST", "/api/config", strings.NewReader(validConfigYAML)))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symlink was replaced (err=%v)", err)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if got, _ := os.ReadFile(target); string(got) != validConfigYAML {
		t.Errorf("target = %q, want %q", got, validConfigYAML)
	}
}
