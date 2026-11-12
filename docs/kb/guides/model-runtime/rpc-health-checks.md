---
title: RPC health checks for distributed inference
summary: Hide and reject a model whose llama.cpp rpc-server backends are unreachable, using rpcHealthCheck.
category: guides
tags: [rpc, rpc-server, distributed, health-check, 503]
config_keys: [models.*.rpcHealthCheck]
updated: 2026-11-12
---

# RPC health checks for distributed inference

A llama-server started with `--rpc host:port,...` offloads layers to
`rpc-server` processes on other machines. If one of them is down, the model
still looks available, and every request starts a load that fails against the
dead backend.

`rpcHealthCheck: true` makes llama-swap watch those endpoints:

```yaml
models:
  qwen-distributed:
    rpcHealthCheck: true
    cmd: |
      llama-server --port ${PORT}
      --rpc 192.168.1.10:50051,192.168.1.11:50051
      -m Qwen2.5-32B-Instruct-Q4_K_M.gguf -ngl 99
```

- The endpoints are read from `--rpc` in `cmd` (`--rpc a,b`, `--rpc=a,b` and
  `-rpc` all work). With no `--rpc` flag the check does nothing.
- Every 10 seconds llama-swap opens a TCP connection to each endpoint, whether
  or not the model is loaded.
- While any endpoint refuses the connection, the model is left out of
  `/v1/models` and requests for it, including `/upstream/<model>/...`, get a
  `503`.
- The model starts out unavailable until the first check succeeds, so it can
  be missing from `/v1/models` for a moment after startup or a config reload.
- A connection attempt that times out keeps the previous state. A backend
  behind a firewall that drops packets never becomes available. Check that
  the llama-swap host can reach the port.
- The check proves the port accepts connections, not that rpc-server is
  healthy or has enough memory for the model.
