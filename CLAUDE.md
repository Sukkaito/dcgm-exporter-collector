# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
make build            # Build both binaries to bin/
make build-agent      # Build only dcgm-compute-agent
make build-control    # Build only dcgm-control-service
make test             # go test -v -race ./...
make vet              # go vet ./...
make docker           # Build both Docker images
make docker-agent     # Build dcgm-compute-agent image
make docker-control   # Build dcgm-control-service image

# Run a single test
go test -v -race -run TestFunctionName ./pkg/processor/

# Run benchmarks
go test -bench=. ./pkg/processor/
```

## Architecture

Two-binary system for collecting NVIDIA GPU metrics from OpenStack VMs with PCI passthrough, without requiring GPU drivers on the compute host.

### Binaries

- **`dcgm-compute-agent`** (`cmd/dcgm-compute-agent/`) — Runs on compute nodes. Zero OpenStack credentials. Provisions veth pairs + OVS ports, scrapes guest `dcgm-exporter` endpoints (:9400), enriches metrics with hypervisor labels, exposes aggregated Prometheus `/metrics` on :9405.
- **`dcgm-control-service`** (`cmd/dcgm-control-service/`) — Runs on control node. Holds OpenStack admin creds. Discovers GPU VMs via Nova flavor extra specs, provisions Neutron host ports. Communicates with compute agents over mTLS (TLS 1.3, client cert CN = compute hostname).

### Data Flow

1. Compute agent sends `POST /api/v1/host/sync` to control service (mTLS)
2. Control service queries Nova for GPU VMs on that host, ensures Neutron host ports
3. Returns `SyncResponse` with network endpoints + scrape targets
4. Compute agent reconciles veth/OVS interfaces inside isolated network namespaces (`dcgm-<short-id>`), then scrapes guest dcgm-exporter endpoints directly within each VM's netns
5. Metrics are parsed, enriched with `host`/`vm_id`/`vm_name`/`project_id` labels, merged, and served

### Key Packages

| Package | Purpose |
|---------|---------|
| `pkg/api` | Shared request/response models (`SyncRequest`, `SyncResponse`, `TargetVM`) |
| `pkg/coordinator` | mTLS sync client + in-memory target registry with periodic sync loop |
| `pkg/controller` | Control node mTLS HTTPS server, sync handler, cert-based host extraction |
| `pkg/network` | Veth pair manager (`ip link`), NetNS manager (`ip netns`), OVS manager (`ovs-vsctl`), endpoint reconciliation |
| `pkg/openstack` | `OpenStackClient` interface, Gophercloud implementation, mock for testing |
| `pkg/processor` | Prometheus text parser/enricher using `prometheus/client_model` protos, merge + encode |
| `pkg/scraper` | Bounded concurrent worker pool scraper with per-target fault isolation |
| `pkg/server` | HTTP server exposing `/metrics`, `/healthz`, `/readyz`, `/status` with response caching |
| `pkg/transport` | `TelemetryTransport` interface, IPv4 HTTP implementation with per-netns socket dialing |
| `internal/config` | Compute agent config: flags > env vars > JSON file, duration parsing |
| `internal/controlconfig` | Control service config: flags > env vars > JSON file |

### Config Precedence

Both binaries: CLI flags override env vars override JSON config file. Durations in JSON accept Go duration strings (`"60s"`, `"3s"`).

- Compute agent: `-config` flag or `DCGM_CONFIG_FILE` env. See `config.sample.json`.
- Control service: `-config` flag or `DCGM_CONTROL_CONFIG_FILE` env. See `config.control.sample.json`.

### Testing with Mock Mode

Control service supports `-use-mock` flag / `DCGM_USE_MOCK_OPENSTACK=true` for local testing without OpenStack. The mock client is in `pkg/openstack/mock.go`.

Compute agent supports `static_targets` in config JSON to skip control-node sync entirely.

### Collector Metrics

The compute agent injects these operational metrics into `/metrics` output:
- `dcgm_collector_scrape_success` — per-VM gauge (0/1)
- `dcgm_collector_scrape_duration_seconds` — per-VM scrape latency
- `dcgm_collector_targets_total` — total targets on host
- `dcgm_collector_targets_healthy` — healthy targets on host

### Network Subsystem

The `pkg/network` package shells out to `ip link`, `ip netns`, and `ovs-vsctl` via `ExecCommandRunner`. For each tenant network, host interfaces (`{veth_name}`, e.g. `net31464df7`) are wrapped into dedicated network namespaces (`dcgm-{short_net_id}`), with loopback and host veth brought UP and assigned IP inside the netns. This isolates routing domains across tenant networks with overlapping CIDRs. OVS peer interfaces (`{veth_name}-ovs`, e.g. `net31464df7-ovs`) remain in the host root namespace and use `external_ids:iface-id` for OVN port binding. All interface names strictly comply with the Linux 15-character `IFNAMSIZ` limit. `NetworkManager.ReconcileEndpoints` is idempotent and cleans up stale interfaces and netns.

## Agent skills

> **Note for maintainers and agents**: The issue tracking configuration, triage roles, domain glossary (`CONTEXT.md`), and architectural decision records (`docs/adr/`) were scaffolded and modeled using the `mattpocock-skills` engineering skillset (`setup-matt-pocock-skills`, `grilling`, and `domain-modeling`).

### Issue tracker

Issues live as markdown files under `.scratch/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical triage roles mapped 1:1. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context repository layout (`CONTEXT.md` + `docs/adr/`). See `docs/agents/domain.md`.


