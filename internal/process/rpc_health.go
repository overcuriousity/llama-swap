package process

import (
	"errors"
	"net"
	"sync/atomic"
	"time"

	"github.com/mostlygeek/llama-swap/internal/config"
)

const (
	rpcHealthCheckInterval = 10 * time.Second
	rpcHealthDialTimeout   = 3 * time.Second
)

// rpcHealth tracks the reachability of the --rpc endpoints declared in a
// model's cmd. It runs independently of the process lifecycle: endpoints are
// polled whether or not the model is loaded, so a request can be rejected
// before a start is attempted against a dead RPC backend.
type rpcHealth struct {
	endpoints []string
	healthy   atomic.Bool

	// dial probes one endpoint; nil uses a TCP dial. Replaced in tests.
	dial func(endpoint string) error
}

// initRPCHealth parses the --rpc endpoints out of the model command and starts
// the background poller. It is a no-op when rpcHealthCheck is not enabled.
func (p *ProcessCommand) initRPCHealth() {
	if !p.config.RPCHealthCheck {
		return
	}

	endpoints, err := config.ParseRPCEndpoints(p.config.Cmd)
	if err != nil {
		p.proxyLogger.Errorf("<%s> failed to parse RPC endpoints: %v", p.id, err)
		return
	}
	if len(endpoints) == 0 {
		p.proxyLogger.Warnf("<%s> rpcHealthCheck enabled but no --rpc flag found in cmd", p.id)
		return
	}

	h := &rpcHealth{endpoints: endpoints}
	h.healthy.Store(false) // start unhealthy until the first check passes
	p.rpcHealth.Store(h)

	go p.pollRPCHealth(h)
}

func (p *ProcessCommand) pollRPCHealth(h *rpcHealth) {
	ticker := time.NewTicker(rpcHealthCheckInterval)
	defer ticker.Stop()

	p.checkRPCHealth(h)

	for {
		select {
		case <-p.parentCtx.Done():
			p.proxyLogger.Debugf("<%s> RPC health checker shutting down", p.id)
			return
		case <-ticker.C:
			p.checkRPCHealth(h)
		}
	}
}

func (p *ProcessCommand) checkRPCHealth(h *rpcHealth) {
	dial := h.dial
	if dial == nil {
		dial = func(endpoint string) error {
			conn, err := net.DialTimeout("tcp", endpoint, rpcHealthDialTimeout)
			if err == nil {
				conn.Close()
			}
			return err
		}
	}

	allHealthy := true
	timedOut := false

	for _, endpoint := range h.endpoints {
		err := dial(endpoint)
		if err == nil {
			continue
		}
		// A dial timeout means the endpoint is slow or silently dropping
		// packets, which this check cannot tell apart. It neither proves nor
		// disproves health, so it leaves the current state alone: a loaded
		// model is not rejected during a transient stall, and an endpoint that
		// has never answered does not become healthy by timing out.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			p.proxyLogger.Debugf("<%s> RPC endpoint %s timeout: %v", p.id, endpoint, err)
			timedOut = true
			continue
		}
		p.proxyLogger.Warnf("<%s> RPC endpoint %s unhealthy: %v", p.id, endpoint, err)
		allHealthy = false
		break
	}

	if allHealthy && timedOut {
		return
	}

	wasHealthy := h.healthy.Swap(allHealthy)

	if wasHealthy && !allHealthy {
		p.proxyLogger.Infof("<%s> RPC endpoints now UNHEALTHY", p.id)
	} else if !wasHealthy && allHealthy {
		p.proxyLogger.Infof("<%s> RPC endpoints now HEALTHY", p.id)
	}
}

// IsRPCHealthy reports whether all configured RPC endpoints are reachable.
// It returns true when RPC health checking is not in use for this model.
func (p *ProcessCommand) IsRPCHealthy() bool {
	h := p.rpcHealth.Load()
	if h == nil {
		return true
	}
	return h.healthy.Load()
}
