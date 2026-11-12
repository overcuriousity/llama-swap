package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/event"
	"github.com/mostlygeek/llama-swap/internal/swaputil"
)

// maxConfigUploadBytes bounds the config document accepted by the editor so a
// malformed or hostile client cannot force an unbounded read into memory.
const maxConfigUploadBytes = 4 << 20 // 4 MiB

// ConfigSource describes where the running config was loaded from and what
// example document the UI should offer as a starting point. It is empty when
// llama-swap was started without a config file, which disables the editor.
type ConfigSource struct {
	// Path is the config file the editor reads and writes.
	Path string

	// Example is the embedded config.example.yaml served to the UI.
	Example []byte
}

// SetConfigSource enables the config editor endpoints. It must be called before
// the server starts serving.
func (s *Server) SetConfigSource(src ConfigSource) {
	s.configSource = src
}

func (s *Server) handleAPIConfigCurrent(w http.ResponseWriter, r *http.Request) {
	path := s.configSource.Path
	if path == "" {
		http.Error(w, "llama-swap-error: config file path not set", http.StatusNotFound)
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		s.logs.ProxyLogs.Errorf("config editor: failed to read %s: %v", path, err)
		http.Error(w, "llama-swap-error: failed to read config file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (s *Server) handleAPIConfigExample(w http.ResponseWriter, r *http.Request) {
	if len(s.configSource.Example) == 0 {
		http.Error(w, "llama-swap-error: example config not available", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(s.configSource.Example)
}

func (s *Server) handleAPIConfigUpdate(w http.ResponseWriter, r *http.Request) {
	path := s.configSource.Path
	if path == "" {
		http.Error(w, "llama-swap-error: config file path not set", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigUploadBytes+1))
	if err != nil {
		http.Error(w, "llama-swap-error: failed to read request body", http.StatusBadRequest)
		return
	}
	if len(body) > maxConfigUploadBytes {
		http.Error(w, "llama-swap-error: config exceeds maximum size", http.StatusRequestEntityTooLarge)
		return
	}

	// Reject documents that do not parse before touching the file on disk. A
	// bad write here takes the proxy down on the next reload.
	if _, err := config.LoadConfigFromReader(strings.NewReader(string(body))); err != nil {
		http.Error(w, fmt.Sprintf("llama-swap-error: invalid config: %v", err), http.StatusBadRequest)
		return
	}

	// Write via a temp file in the same directory and rename, so a crash or a
	// concurrent reader never observes a half-written config.
	if err := writeFileAtomic(path, body); err != nil {
		s.logs.ProxyLogs.Errorf("config editor: failed to write %s: %v", path, err)
		http.Error(w, "llama-swap-error: failed to write config file", http.StatusInternalServerError)
		return
	}

	event.Emit(swaputil.ConfigFileChangedEvent{State: swaputil.ReloadingStateStart})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "Config updated successfully. Reloading...",
	})
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".llama-swap-config-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// CreateTemp makes the file 0600; match the 0644 the config normally has.
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
