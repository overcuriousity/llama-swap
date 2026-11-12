package process

import (
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
	"github.com/mostlygeek/llama-swap/internal/logmon"
)

func newRPCTestProcess(t *testing.T, conf config.ModelConfig) *ProcessCommand {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p, err := New(ctx, "test-model", conf, logmon.NewWriter(io.Discard), logmon.NewWriter(io.Discard))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestProcessCommand_RPCHealthDisabledIsAlwaysHealthy(t *testing.T) {
	p := newRPCTestProcess(t, config.ModelConfig{
		Cmd:            "llama-server --rpc 127.0.0.1:1 -ngl 99",
		RPCHealthCheck: false,
	})

	if !p.IsRPCHealthy() {
		t.Fatal("expected a process without rpcHealthCheck to report healthy")
	}
}

func TestProcessCommand_RPCHealthNoEndpointsIsAlwaysHealthy(t *testing.T) {
	// rpcHealthCheck is on but the command has no --rpc flag: there is nothing
	// to poll, so the model must not be gated off.
	p := newRPCTestProcess(t, config.ModelConfig{
		Cmd:            "llama-server -ngl 99",
		RPCHealthCheck: true,
	})

	if !p.IsRPCHealthy() {
		t.Fatal("expected a process with no RPC endpoints to report healthy")
	}
}

func TestProcessCommand_RPCHealthReachableEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	p := newRPCTestProcess(t, config.ModelConfig{
		Cmd:            fmt.Sprintf("llama-server --rpc %s -ngl 99", ln.Addr().String()),
		RPCHealthCheck: true,
	})

	waitForRPCHealth(t, p, true)
}

func TestProcessCommand_RPCHealthUnreachableEndpoint(t *testing.T) {
	// Bind then immediately release the port so nothing is listening on it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	p := newRPCTestProcess(t, config.ModelConfig{
		Cmd:            fmt.Sprintf("llama-server --rpc %s -ngl 99", addr),
		RPCHealthCheck: true,
	})

	waitForRPCHealth(t, p, false)
}

func waitForRPCHealth(t *testing.T, p *ProcessCommand, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p.IsRPCHealthy() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("IsRPCHealthy() = %v, want %v", p.IsRPCHealthy(), want)
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestProcessCommand_RPCHealthTimeoutKeepsState(t *testing.T) {
	p := newRPCTestProcess(t, config.ModelConfig{Cmd: "llama-server -ngl 99"})
	refused := fmt.Errorf("connection refused")

	results := map[string]error{"a:1": nil, "b:1": nil}
	h := &rpcHealth{
		endpoints: []string{"a:1", "b:1"},
		dial:      func(ep string) error { return results[ep] },
	}

	// An endpoint that has never answered must not turn healthy by timing out.
	results["b:1"] = timeoutErr{}
	p.checkRPCHealth(h)
	if h.healthy.Load() {
		t.Fatal("timeout on first check reported healthy")
	}

	results["b:1"] = nil
	p.checkRPCHealth(h)
	if !h.healthy.Load() {
		t.Fatal("all endpoints reachable but reported unhealthy")
	}

	// A transient stall on a healthy model leaves it healthy.
	results["a:1"] = timeoutErr{}
	p.checkRPCHealth(h)
	if !h.healthy.Load() {
		t.Fatal("timeout flipped a healthy model to unhealthy")
	}

	// A hard failure wins over a timeout on another endpoint.
	results["b:1"] = refused
	p.checkRPCHealth(h)
	if h.healthy.Load() {
		t.Fatal("refused endpoint reported healthy")
	}
}
