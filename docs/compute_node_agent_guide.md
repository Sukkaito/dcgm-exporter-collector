# Compute-Node Collector Agent Deployment Guide

This guide describes how to configure, deploy, and verify the `dcgm-compute-agent` on OpenStack compute nodes deployed via Kolla-Ansible.

---

## 1. Overview

`dcgm-compute-agent` is a unified single-binary daemon running on each participating compute host. It operates without OpenStack API credentials and performs two primary duties:

1. **Network Provisioner**: Periodically contacts the control-node sync controller via HTTPS/mTLS (`POST /api/v1/host/sync`), reconciles persistent veth pairs, and attaches them to Open vSwitch (`br-int`) using the local `ovs-vsctl` client.
2. **GPU Telemetry Collector**: Scrapes guest `dcgm-exporter` instances concurrently across passthrough VMs using local IPv4, enriches metrics with host and VM metadata, and exposes a Prometheus-compatible `/metrics` endpoint.

```text
                           CONTROL NODE
                  +----------------------------+
                  | host-network-controller    |
                  | (OpenStack Admin / Nova)   |
                  +-------------^--------------+
                                |
                         HTTPS + mTLS
                                |
                  +-------------+--------------+
                  |         COMPUTE NODE       |
                  |                            |
                  |    dcgm-compute-agent      |
                  |  +-----------------------+ |
                  |  | Coordinator & Sync    | |
                  |  | Network Provisioner   | |
                  |  | Telemetry Collector   | |
                  |  +-----------+-----------+ |
                  |              |             |
                  |     veth pair via ovs-vsctl|
                  |              v             |
                  |            br-int          |
                  |              |             |
                  |        VM Local IPv4       |
                  |              v             |
                  |     guest dcgm-exporter    |
                  |       (:9400/metrics)      |
                  +--------------+-------------+
                                 |
                         GET :9405/metrics
                                 v
                     Prometheus Scraper
```

---

## 2. Prerequisites & Host Privileges

The agent requires:
- **Network Capabilities**: `CAP_NET_ADMIN` (or root execution) to manage veth interfaces and IP addresses via Linux netlink / `ip`.
- **Open vSwitch CLI**: Access to execute `ovs-vsctl` to manage ports on `br-int`.
- **Network Access**:
  - Outbound HTTPS to the control-node controller.
  - Inbound local IPv4 to guest VM ports (typically port `9400`).
  - Inbound HTTP on port `:9405` for Prometheus metric collection.
- **Zero Host GPU Requirements**: The compute host does **not** need the NVIDIA driver, NVML, or DCGM libraries installed.

---

## 3. Configuration Reference

Configuration can be supplied via command-line flags, environment variables, or a JSON configuration file.

| Flag | Environment Variable | Default | Description |
|---|---|---|---|
| `-controller-url` | `DCGM_CONTROLLER_URL` | `""` | HTTPS URL of the control node sync API |
| `-hostname` | `DCGM_HOSTNAME` | Hostname | Compute node identifier for sync & metric labeling |
| `-ca-cert` | `DCGM_CA_CERT` | `""` | Path to CA certificate for verifying the controller |
| `-cert` | `DCGM_CLIENT_CERT` | `""` | Path to client certificate for mTLS |
| `-key` | `DCGM_CLIENT_KEY` | `""` | Path to client private key for mTLS |
| `-sync-interval` | `DCGM_SYNC_INTERVAL` | `60s` | Interval between controller synchronization requests |
| `-sync-timeout` | `DCGM_SYNC_TIMEOUT` | `10s` | HTTP timeout for control node sync calls |
| `-listen-addr` | `DCGM_LISTEN_ADDR` | `:9405` | HTTP listen address for `/metrics` and status |
| `-scrape-timeout` | `DCGM_SCRAPE_TIMEOUT` | `3s` | Per-VM guest exporter scrape timeout |
| `-cache-ttl` | `DCGM_CACHE_TTL` | `5s` | Metric exposition cache duration |
| `-ovs-bridge` | `DCGM_OVS_BRIDGE` | `br-int` | Open vSwitch integration bridge name |
| `-ovs-vsctl` | `DCGM_OVS_VSCTL` | `ovs-vsctl` | Executable path to `ovs-vsctl` |
| `-config` | `DCGM_CONFIG_FILE` | `""` | Path to optional JSON configuration file |

---

## 4. CA & mTLS Certificate Configuration Guide

The compute agent uses mutually authenticated TLS (mTLS) to communicate securely with the control node sync API (`POST /api/v1/host/sync`).

- **CA Certificate (`ca_cert_path`)**: Verifies the authenticity of the control node server certificate.
- **Client Certificate (`cert_path`)**: Identifies the compute host to the control node. The Common Name (CN) or SAN must match the compute hostname.
- **Client Private Key (`key_path`)**: The private key corresponding to the client certificate.

### 4.1 Generating Certificates (Example with OpenSSL)

If using an internal PKI or self-signed CA:

1. **Create the Certificate Authority (on control node or secure workstation)**:
   ```bash
   openssl req -x509 -newkey rsa:4096 -days 3650 -nodes \
     -keyout ca.key -out ca.crt \
     -subj "/CN=DCGM-Telemetry-CA"
   ```

2. **Generate Compute Node Private Key and CSR (on compute node)**:
   ```bash
   COMPUTE_HOST="hgx087.compute.internal"

   openssl req -newkey rsa:2048 -nodes \
     -keyout client.key -out client.csr \
     -subj "/CN=${COMPUTE_HOST}"
   ```

3. **Sign Compute Node Client Certificate with CA**:
   ```bash
   openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
     -out client.crt -days 365 -sha256
   ```

### 4.2 File Permissions & Placement

Place certificates in `/etc/dcgm-compute-agent/certs/` and restrict permissions:
```bash
mkdir -p /etc/dcgm-compute-agent/certs
chmod 700 /etc/dcgm-compute-agent/certs

cp ca.crt /etc/dcgm-compute-agent/certs/ca.crt
cp client.crt /etc/dcgm-compute-agent/certs/client.crt
cp client.key /etc/dcgm-compute-agent/certs/client.key

chmod 644 /etc/dcgm-compute-agent/certs/ca.crt
chmod 644 /etc/dcgm-compute-agent/certs/client.crt
chmod 600 /etc/dcgm-compute-agent/certs/client.key
```

### 4.3 Verifying mTLS Connectivity with Curl

Test connectivity from the compute host to the control node before starting the agent:
```bash
curl --cacert /etc/dcgm-compute-agent/certs/ca.crt \
     --cert /etc/dcgm-compute-agent/certs/client.crt \
     --key /etc/dcgm-compute-agent/certs/client.key \
     -X POST https://control-node.internal:8443/api/v1/host/sync \
     -H "Content-Type: application/json" \
     -d '{"action":"sync"}'
```
A successful connection returns HTTP 200 with the `SyncResponse` payload containing network endpoints and VM targets.

---

## 5. Deployment Methods

### Option A: Systemd Service (Recommended)

1. Build or install the binary:
   ```bash
   cp bin/dcgm-compute-agent /usr/local/bin/
   chmod 755 /usr/local/bin/dcgm-compute-agent
   ```

2. Create the configuration directory and credentials:
   ```bash
   mkdir -p /etc/dcgm-compute-agent/certs
   chmod 700 /etc/dcgm-compute-agent/certs
   # Copy ca.crt, client.crt, client.key into /etc/dcgm-compute-agent/certs/
   ```

3. Create configuration file `/etc/dcgm-compute-agent/agent.json`:
   ```json
   {
     "controller_url": "https://control-node.internal:8443",
     "ca_cert_path": "/etc/dcgm-compute-agent/certs/ca.crt",
     "cert_path": "/etc/dcgm-compute-agent/certs/client.crt",
     "key_path": "/etc/dcgm-compute-agent/certs/client.key",
     "listen_addr": ":9405",
     "ovs_bridge": "br-int",
     "sync_interval": "60s"
   }
   ```

4. Create systemd unit `/etc/systemd/system/dcgm-compute-agent.service`:
   ```ini
   [Unit]
   Description=DCGM Exporter Compute Collector Agent
   After=network.target openvswitch-switch.service
   Wants=network.target

   [Service]
   Type=simple
   ExecStart=/usr/local/bin/dcgm-compute-agent -config /etc/dcgm-compute-agent/agent.json
   Restart=always
   RestartSec=5s
   AmbientCapabilities=CAP_NET_ADMIN
   CapabilityBoundingSet=CAP_NET_ADMIN

   [Install]
   WantedBy=multi-user.target
   ```

5. Enable and start:
   ```bash
   systemctl daemon-reload
   systemctl enable --now dcgm-compute-agent
   ```

---

### Option B: Docker Container on Kolla-Ansible Compute Host

When running alongside Kolla containers:
#### 1. Build the Container Image
Build using `make`, the dedicated `Dockerfile.compute-agent`, or the root `Dockerfile`:
```bash
# Option 1: Using Makefile
make docker-agent

# Option 2: Using Dockerfile.compute-agent
docker build -t dcgm-compute-agent:latest -f Dockerfile.compute-agent .

# Option 3: Using root Dockerfile (defaults to compute-agent)
docker build -t dcgm-compute-agent:latest .
```

#### 2. Run the Container alongside Kolla
```bash
docker run -d \
  --name dcgm_compute_agent \
  --restart unless-stopped \
  --network host \
  --cap-add NET_ADMIN \
  -v /var/run/openvswitch:/var/run/openvswitch:ro \
  -v /var/run/openvswitch:/var/run/openvswitch \
  -v /etc/dcgm-compute-agent:/etc/dcgm-compute-agent:ro \
  dcgm-compute-agent:latest \
  -config /etc/dcgm-compute-agent/agent.json
```

**Key Container Parameters**:
- `--network host`: Attaches directly to the host network namespace to manage local veth interfaces and expose port `9405`.
- `--cap-add NET_ADMIN`: Grants Linux network management permissions needed by `ip link` / `ip addr`.
- `-v /var/run/openvswitch:/var/run/openvswitch`: Mounts the host OVS runtime directory so internal `ovs-vsctl` can connect to the Open vSwitch daemon socket (`db.sock`).
- `-v /etc/dcgm-compute-agent:/etc/dcgm-compute-agent:ro`: Mounts the agent configuration and mTLS certificates.

---

## 6. Verification & Observability

### Health Checks
- Process liveness:
  ```bash
  curl -s http://localhost:9405/healthz
  # Expected output: OK
  ```

- Process readiness:
  ```bash
  curl -s http://localhost:9405/readyz
  # Expected output: READY
  ```

### Target Status Report
Inspect the operational state of all monitored VMs and guest exporters:
```bash
curl -s http://localhost:9405/status | jq .
```
Example response:
```json
{
  "collector": "healthy",
  "timestamp": "2026-09-22T12:00:00Z",
  "total_targets": 2,
  "healthy_targets": 2,
  "exporters": [
    {
      "vm_id": "4b6ec340-e448-4395-927a-59fa3e7dc957",
      "vm_name": "gpu-worker-01",
      "endpoint": "10.0.0.12:9400",
      "healthy": true,
      "last_success": "2026-09-22T12:00:00Z",
      "scrape_duration_ms": 14
    }
  ]
}
```

### Prometheus Metrics Exposition
```bash
curl -s http://localhost:9405/metrics | grep DCGM_FI_DEV_GPU_UTIL
```
Example enriched metric output:
```text
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{UUID="GPU-4c19ad77-3e1b-...",device="nvidia0",gpu="0",host="hgx087",modelName="NVIDIA A100-SXM4-40GB",project_id="6d149c...",vm_id="4b6ec340...",vm_name="gpu-worker-01"} 78
```

---

## 7. Fault Isolation & Recovery

- **Unreachable VM / Guest Exporter Restart**: The collector isolates failures per VM using a 3-second timeout. An unreachable VM does not delay or block scrapes for other VMs. Its status in `/status` will indicate `healthy: false` with the exact connection error while healthy VMs continue serving metrics.
- **Compute Host Reboot**: The agent starts on boot, synchronizes state from the control node, recreates the veth pair and attaches it to `br-int` idempotently.
- **Control Node Outage**: Existing host veth pairs and OVS attachments remain active; the agent continues scraping existing VMs and retries control node sync periodically.
