package server

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// maxConfigUploadBytes bounds the config document accepted by the editor so a
// malformed or hostile client cannot force an unbounded read into memory.
const maxConfigUploadBytes = 4 << 20 // 4 MiB

// configWriteMu serializes editor writes. Validation reads the -config-dir
// files and the write replaces the config file; two saves racing could
// otherwise each validate against a state the other is about to change. It is
// package level because a hot reload replaces the Server but not the file.
var configWriteMu sync.Mutex

// ConfigSource describes where the running config was loaded from and what
// example document the UI should offer as a starting point. Path is empty when
// llama-swap was started without -config, which disables the editor.
type ConfigSource struct {
	// Path is the config file the editor reads and writes.
	Path string

	// Dir is the -config-dir merged on top of Path, if any. A saved document
	// is validated together with it, exactly as it will be loaded.
	Dir string

	// Example is the example configuration served to the UI.
	Example []byte
}

// SetConfigSource enables the config editor endpoints. It must be called before
// the server starts serving.
func (s *Server) SetConfigSource(src ConfigSource) {
	s.configSource = src
}

// sameOriginOnly rejects requests a browser made on behalf of another site.
//
// The editor reads a file that can hold API keys and writes one whose cmd
// entries llama-swap executes. CORS does not protect that: the default policy
// allows every origin, and even a strict one only stops a page from reading
// the response, not a simple cross-site POST from being sent. So any page the
// user visits could otherwise replace the config with one that runs its own
// command. The UI is served by llama-swap itself, so its requests are always
// same-origin.
//
// Browsers mark every request with Sec-Fetch-Site. When it is missing (older
// browsers, curl, scripts) the Origin header, which browsers do send on
// cross-origin requests, must match the requested host. A request with neither
// did not come from a web page and is left to the auth middleware.
func sameOriginOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
			if site != "same-origin" && site != "none" {
				swaputil.SendResponse(w, r, http.StatusForbidden, "config editor only accepts same-origin requests")
				return
			}
		} else if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(u.Host, r.Host) {
				swaputil.SendResponse(w, r, http.StatusForbidden, "config editor only accepts same-origin requests")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleAPIConfigCurrent(w http.ResponseWriter, r *http.Request) {
	path := s.configSource.Path
	if path == "" {
		swaputil.SendResponse(w, r, http.StatusNotFound, "config editor disabled: llama-swap was started without -config")
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		s.logs.ProxyLogs.Errorf("config editor: failed to read %s: %v", path, err)
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to read config file")
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) handleAPIConfigExample(w http.ResponseWriter, r *http.Request) {
	if len(s.configSource.Example) == 0 {
		swaputil.SendResponse(w, r, http.StatusNotFound, "example config not available")
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(s.configSource.Example)
}

func (s *Server) handleAPIConfigUpdate(w http.ResponseWriter, r *http.Request) {
	path := s.configSource.Path
	if path == "" {
		swaputil.SendResponse(w, r, http.StatusNotFound, "config editor disabled: llama-swap was started without -config")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigUploadBytes+1))
	if err != nil {
		swaputil.SendResponse(w, r, http.StatusBadRequest, "failed to read request body")
		return
	}
	if len(body) > maxConfigUploadBytes {
		swaputil.SendResponse(w, r, http.StatusRequestEntityTooLarge, "config exceeds maximum size")
		return
	}

	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	// Write to the real file (through any symlink, e.g. a mounted ConfigMap)
	// so the rename below replaces the target rather than the link.
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		s.logs.ProxyLogs.Errorf("config editor: failed to resolve %s: %v", path, err)
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to resolve config file")
		return
	}

	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm()
	}

	tmpName, err := writeTempConfig(target, body, mode)
	if err != nil {
		s.logs.ProxyLogs.Errorf("config editor: failed to write %s: %v", path, err)
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to write config file")
		return
	}
	defer os.Remove(tmpName)

	// Validate with the same loader startup and reload use, merged with
	// -config-dir, before the file on disk is touched. A document that only
	// fails once merged would otherwise be accepted here and then rejected by
	// the reload, leaving a file on disk that the next restart cannot load.
	if _, err := config.LoadConfigSources(tmpName, s.configSource.Dir); err != nil {
		// Name the user's file in the message, not the temp copy.
		msg := strings.ReplaceAll(err.Error(), tmpName, path)
		swaputil.SendResponse(w, r, http.StatusBadRequest, "invalid config: "+msg)
		return
	}

	if err := os.Rename(tmpName, target); err != nil {
		s.logs.ProxyLogs.Errorf("config editor: failed to replace %s: %v", path, err)
		swaputil.SendResponse(w, r, http.StatusInternalServerError, "failed to write config file")
		return
	}

	s.logs.ProxyLogs.Infof("config editor: %s updated from %s", path, r.RemoteAddr)
	event.Emit(swaputil.ConfigFileChangedEvent{State: swaputil.ReloadingStateStart})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Config updated successfully. Reloading...",
	})
}

// writeTempConfig writes data to a new file next to target and returns its
// name. Same directory so the later rename is atomic: a crash or a concurrent
// reader never sees a half-written config. The name has no .yaml extension so
// a -config-dir that contains the config's directory does not pick it up.
func writeTempConfig(target string, data []byte, mode fs.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".llama-swap-config-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(name)
		return "", err
	}

	if _, err := tmp.Write(data); err != nil {
		return fail(err)
	}
	if err := tmp.Sync(); err != nil {
		return fail(err)
	}
	// Keep the permissions the config had: it may hold API keys and be
	// deliberately unreadable to other users.
	if err := tmp.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}
