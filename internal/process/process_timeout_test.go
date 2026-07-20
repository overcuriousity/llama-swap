package process

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

// A request that outlives requestTimeout must not leave the upstream pinned:
// the process is stopped so it stops holding the GPU.
func TestProcessCommand_RequestTimeoutStopsProcess(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 15,
		UnloadTimeout:      2,
		RequestTimeout:     1,
	})

	runErr := runAsync(t, p)

	// 60 chars echoed 200ms apart is ~12s, far past the 1s requestTimeout.
	req := httptest.NewRequest("GET", "/slow-respond?delay=200ms", nil)
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	deadline := time.Now().Add(testStartTimeout)
	for time.Now().Before(deadline) {
		if p.State() == StateStopped {
			break
		}
		time.Sleep(testPollInterval)
	}

	if got := p.State(); got != StateStopped {
		t.Fatalf("State() = %v, want %v after requestTimeout fired", got, StateStopped)
	}

	select {
	case <-runErr:
	case <-time.After(testStopTimeout):
		t.Fatal("Run did not return after the process was stopped")
	}
}

// requestTimeout of 0 (the default) must leave request handling untouched.
func TestProcessCommand_RequestTimeoutDisabled(t *testing.T) {
	skipIfNoSimpleResponder(t)

	cmd, port := simpleResponderCmd(t, "-silent", "-respond hello")
	p := newProcessCommand(t, config.ModelConfig{
		Cmd:                cmd,
		Proxy:              fmt.Sprintf("http://127.0.0.1:%d", port),
		CheckEndpoint:      "/health",
		HealthCheckTimeout: 15,
		UnloadTimeout:      2,
		RequestTimeout:     0,
	})

	runAsync(t, p)
	defer p.Stop(testStopTimeout)

	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if p.State() != StateReady {
		t.Fatalf("State() = %v, want %v", p.State(), StateReady)
	}
}
