package process

import (
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
	allHealthy := true

	for _, endpoint := range h.endpoints {
		dialer := net.Dialer{Timeout: rpcHealthDialTimeout}
		conn, err := dialer.Dial("tcp", endpoint)
		if err != nil {
			// A dial timeout means the endpoint is slow, not gone. Treating it
			// as unhealthy causes loaded models to be rejected during transient
			// network stalls, so only hard failures flip the flag.
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				p.proxyLogger.Debugf("<%s> RPC endpoint %s timeout (ignoring): %v", p.id, endpoint, err)
				continue
			}
			p.proxyLogger.Warnf("<%s> RPC endpoint %s unhealthy: %v", p.id, endpoint, err)
			allHealthy = false
			break
		}
		conn.Close()
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
