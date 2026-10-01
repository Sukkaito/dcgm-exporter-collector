# Per-Tenant Network Namespace Isolation

Host Network Endpoints are isolated inside dedicated Linux network namespaces on compute hypervisors, rather than placing host veth interfaces directly into the host root namespace.

## Status

Accepted

## Context

Target VMs reside in OpenStack tenant networks that may use overlapping private IPv4 CIDRs (e.g. multiple tenants using `10.0.0.0/24`). Placing host-side veth endpoints into the root network namespace would cause routing table conflicts, asymmetric return paths, and cross-tenant traffic leakage.

## Decision

For each tenant network containing GPU Target VMs on a compute host, the Compute Agent creates an isolated Linux network namespace named `dcgm-{short_net_id}`. The host veth interface is placed inside this namespace, assigned an IP in the tenant subnet, and brought up. The Compute Agent dials Guest Exporters directly within each target's network namespace (via per-namespace socket dialing or `setns`).

## Consequences

- Routing domains remain strictly isolated per tenant network, fully supporting overlapping CIDRs without collision.
- The Compute Agent requires Linux network capabilities (`CAP_NET_ADMIN` / `CAP_SYS_ADMIN` or running as root) to manage netns and dial sockets inside them.
- Interface naming must adhere strictly to Linux's 15-character `IFNAMSIZ` limit (using truncated hashes like `net{short_net_id}`).
