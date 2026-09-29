# Compute-Initiated mTLS Host Sync

The Compute Agent periodically polls the Control Service over mutual TLS (TLS 1.3) to retrieve Target VMs and Host Network Endpoints, rather than having the Control Service push configurations to hypervisors or giving compute nodes direct OpenStack API credentials.

## Status

Accepted

## Context

Nova compute hypervisors must collect GPU telemetry from passthrough VMs across tenant networks. Giving compute nodes OpenStack administrative credentials to query Nova/Neutron directly creates a major security exposure if a hypervisor is compromised. Conversely, having a central service push commands into compute nodes requires opening inbound listening ports and management access on hypervisors.

## Decision

The Compute Agent initiates a periodic HTTP POST request to the Control Service's `/api/v1/host/sync` endpoint over mTLS with TLS 1.3. The client certificate's Common Name (CN) identifies the compute hostname. The Control Service handles all OpenStack API operations centrally, ensures Neutron host ports exist, and returns the current set of Target VMs and Host Network Endpoints for that host.

## Consequences

- Compute nodes require zero OpenStack credentials and expose no inbound management ports.
- The Control Service authenticates compute nodes using certificate validation and limits returned data to targets on the requesting host.
- Scrape configuration updates have a delay bounded by the host sync interval (default 60s).
