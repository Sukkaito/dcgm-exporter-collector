# GPU Telemetry Collector

An out-of-band telemetry collection system that gathers GPU metrics from OpenStack virtual machines with PCI passthrough without requiring GPU drivers or tenant network access from the hypervisor host.

## Language

### Core Components

**Compute Host**:
The physical hardware machine and hypervisor operating system where the Compute Agent runs.
_Avoid_: Compute Server, Node Server, Baremetal Server, Server (unqualified)

**Compute Agent**:
A daemon running on hypervisor compute nodes that scrapes guest telemetry endpoints and enriches metrics with hypervisor metadata.
_Avoid_: Node Collector, Central Controller, Collector Agent

**Control Service**:
A central management service that discovers Target VMs via OpenStack and provisions Host Network Endpoints for compute nodes.
_Avoid_: Central Controller, Control Coordinator, Master Service

### Workloads & Endpoints

**Target VM**:
An OpenStack Nova server provisioned with PCI passthrough GPU devices that exposes a guest telemetry endpoint. Specifically refers to a Nova-managed compute instance; virtual machines created directly on the hypervisor outside Nova (e.g. via direct libvirt/virsh) are not Target VMs and are never discovered or scraped.
_Avoid_: Guest Instance, Local VM, Passthrough VM, Worker VM, GPU Instance, Server (unqualified)


**Guest Exporter**:
A daemon running inside a Target VM that exposes GPU telemetry in Prometheus format.
_Avoid_: In-guest Agent, DCGM Daemon, VM Exporter

**Host Network Endpoint**:
A dedicated host-side network interface and port binding created inside a tenant network namespace to reach Target VMs.
_Avoid_: Probe Port, Scrape Gateway, Tenant Tap, Host Port

**Scrape Target**:
The network socket address and namespace tuple where the Compute Agent polls a Guest Exporter.
_Avoid_: Telemetry Endpoint, Worker Job, Scrape Tuple

### Networking & Isolation

**Tenant Network Namespace**:
A Linux network namespace on the compute node that isolates the routing domain and Host Network Endpoint of a specific tenant network.
_Avoid_: Telemetry Namespace, Scrape NetNS, Isolation Sandbox

**OVS Peer Interface**:
The host root namespace interface of a veth pair connected to the Open vSwitch integration bridge with OVN port bindings.
_Avoid_: Switch Port Interface, Host Tap, OVS Trunk Port

### Operations & Processing

**Host Sync**:
The periodic compute-initiated exchange where a Compute Agent fetches active Target VMs and Host Network Endpoints from the Control Service.
_Avoid_: Host Synchronization, Node Heartbeat, Target Polling, Host Registration

**Metric Enrichment**:
The addition of Hypervisor Metadata to raw metrics scraped from Guest Exporters prior to serving them.
_Avoid_: Label Tagging, Metric Injection, Host Decoration

**Hypervisor Metadata**:
Authoritative identity labels added by the hypervisor (including host name, VM identifier, and project identifier).
_Avoid_: Host Tags, Injected Labels, Instance Attributes

**Collector Metrics**:
Internal operational Prometheus metrics exposed by the Compute Agent describing scrape latencies, successes, and target counts.
_Avoid_: Internal Telemetry, Agent Status Metrics, Scrape Health Series


