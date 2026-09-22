# Control-Node Collector Service Deployment Guide

This guide describes how to configure, deploy, and verify the `dcgm-control-service` on OpenStack control nodes deployed via Kolla-Ansible.

---

## 1. Overview

`dcgm-control-service` runs on an OpenStack control node and handles synchronization requests from `dcgm-compute-agent` daemons running across compute hosts.

Key responsibilities:
1. **Compute Authentication via mTLS**: Validates compute client certificates against a trusted CA and extracts the compute hostname directly from the certificate Subject Common Name (`CN`).
2. **GPU Passthrough VM Discovery**: Queries Nova for active servers on the requesting compute host and inspects flavor extra specs (`pci_passthrough:alias`) to identify instances with assigned GPUs.
3. **Neutron Host Port Provisioning**: Automatically discovers the Neutron tenant networks used by those VMs, ensures a persistent host-side Neutron port exists for each network bound to the compute host (`binding:host_id`), and returns the allocated IP and MAC addresses.
4. **Target Synchronization**: Returns desired network endpoints and VM scrape targets to the compute agent.

```text
                            COMPUTE HOST (hgx087)
                                    |
                           POST /api/v1/host/sync
                     (Client Cert: CN=hgx087)
                                    |
                                    v
                     +----------------------------+
                     |    dcgm-control-service    |
                     |  (:8443, mTLS Enforced)    |
                     +--------------+-------------+
                                    |
            +-----------------------+-----------------------+
            |                                               |
            v                                               v
    Nova API (Keystone Auth)                     Neutron API (Keystone Auth)
- List servers where host=hgx087              - Query VM port fixed IPs
- Inspect flavor extra_specs                  - Find or create persistent host port
  (pci_passthrough:alias: gpu)                  (binding:host_id = hgx087)
```

---

## 2. OpenStack Credentials & Kolla-Ansible Environment

The control service requires OpenStack administrative credentials to query Nova and create Neutron ports.

In a Kolla-Ansible deployment, these credentials are generated in `/etc/kolla/admin-openrc.sh`:
```bash
source /etc/kolla/admin-openrc.sh
```

The service supports both environment variables and a JSON configuration file:

| Variable | Config File Key | Description |
|---|---|---|
| `OS_AUTH_URL` | `auth_url` | Keystone authentication endpoint (e.g. `https://internal.openstack:5000/v3`) |
| `OS_USERNAME` | `username` | Keystone admin username (e.g. `admin`) |
| `OS_PASSWORD` | `password` | Keystone admin password |
| `OS_PROJECT_NAME` | `project_name` | Keystone admin project (e.g. `admin`) |
| `OS_USER_DOMAIN_NAME` | `user_domain_name` | User domain (default: `Default`) |
| `OS_PROJECT_DOMAIN_NAME` | `project_domain_name` | Project domain (default: `Default`) |
| `OS_REGION_NAME` | `region` | OpenStack region (e.g. `RegionOne`) |

---

## 3. GPU Passthrough Detection

To determine which VMs on the compute host have GPU passthrough, the service inspects the flavor extra specs of each active VM.

An instance is identified as a GPU workload if either condition is met:
1. **Flavor Family**: The extra spec `family` equals `gpu` (case-insensitive).
2. **PCI Alias Key**: The extra spec `pci_passthrough:alias` exists and is non-empty, regardless of the GPU model name (e.g., `RTX4090:1`, `A100:1`, `H100:1`, `L40S:1`).

Configuration settings:
- **`extra_specs_key`** (default `pci_passthrough:alias`): The flavor extra spec key to inspect.
- **`extra_specs_keyword`** (default `""`): Substring match required within the alias value. By default, this is empty, matching any alias presence regardless of GPU naming.

---

## 4. CA & mTLS Certificate Setup

The control service requires TLS 1.3 and enforces client certificate verification (`tls.RequireAndVerifyClientCert`).

### 4.1 Server and CA Certificate Placement

Create directory `/etc/dcgm-control-service/certs/`:
```bash
mkdir -p /etc/dcgm-control-service/certs
chmod 700 /etc/dcgm-control-service/certs

# Copy control service certificates
cp ca.crt /etc/dcgm-control-service/certs/ca.crt
cp server.crt /etc/dcgm-control-service/certs/server.crt
cp server.key /etc/dcgm-control-service/certs/server.key

chmod 644 /etc/dcgm-control-service/certs/ca.crt
chmod 644 /etc/dcgm-control-service/certs/server.crt
chmod 600 /etc/dcgm-control-service/certs/server.key
```

### 4.2 Issuing Client Certificates for Compute Nodes

When adding a new compute host (e.g., `hgx087`):
```bash
openssl req -newkey rsa:2048 -nodes \
  -keyout hgx087.key -out hgx087.csr \
  -subj "/CN=hgx087"

openssl x509 -req -in hgx087.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out hgx087.crt -days 365 -sha256
```
Distribute `ca.crt`, `hgx087.crt`, and `hgx087.key` to the compute host.

---

## 5. Configuration Reference

Sample configuration `/etc/dcgm-control-service/control.json`:
```json
{
  "listen_addr": ":8443",
  "tls_cert_path": "/etc/dcgm-control-service/certs/server.crt",
  "tls_key_path": "/etc/dcgm-control-service/certs/server.key",
  "client_ca_cert_path": "/etc/dcgm-control-service/certs/ca.crt",
  "auth_url": "https://keystone.internal:5000/v3",
  "username": "admin",
  "password": "your-openstack-admin-password",
  "project_name": "admin",
  "user_domain_name": "Default",
  "project_domain_name": "Default",
  "region": "RegionOne",
  "extra_specs_key": "pci_passthrough:alias",
  "extra_specs_keyword": "gpu",
  "use_mock": false
}
```

---

## 6. Systemd Service Deployment

1. Install the binary:
   ```bash
   cp bin/dcgm-control-service /usr/local/bin/
   chmod 755 /usr/local/bin/dcgm-control-service
   ```

2. Create systemd unit `/etc/systemd/system/dcgm-control-service.service`:
   ```ini
   [Unit]
   Description=DCGM Exporter Control Service
   After=network.target
   Wants=network.target

   [Service]
   Type=simple
   ExecStart=/usr/local/bin/dcgm-control-service -config /etc/dcgm-control-service/control.json
   Restart=always
   RestartSec=5s

   [Install]
   WantedBy=multi-user.target
   ```

3. Enable and start:
   ```bash
   systemctl daemon-reload
   systemctl enable --now dcgm-control-service
   ```

---

## 7. Verification & Testing

### 7.1 Liveness and Readiness
```bash
curl -k https://localhost:8443/healthz
# Expected: OK

curl -k https://localhost:8443/readyz
# Expected: READY (verifies OpenStack connectivity)
```

### 7.2 Testing mTLS Host Synchronization
Simulate a request from compute node `hgx087` using its client certificate:
```bash
curl --cacert /etc/dcgm-control-service/certs/ca.crt \
     --cert /path/to/hgx087.crt \
     --key /path/to/hgx087.key \
     -X POST https://localhost:8443/api/v1/host/sync \
     -H "Content-Type: application/json" \
     -d '{"action":"sync"}' | jq .
```

Example response:
```json
{
  "status": "ok",
  "endpoints": [
    {
      "network_id": "31464df7-873b-48ae-823d-b2a3fa439f76",
      "port_id": "89ec937a-4299-4d62-a5ec-9f5b2b2b1897",
      "mac": "fa:16:3e:ab:cd:ef",
      "ip": "10.0.0.254/24",
      "veth_name": "host-net-31464df7"
    }
  ],
  "targets": [
    {
      "vm_id": "a67117f3-d0ea-45a8-8b77-cfc81534062a",
      "vm_name": "llm-inference-01",
      "project_id": "6d149cf2-7634-4bc1-9a74-d4b967814b7e",
      "guest_ip": "10.0.0.15",
      "port": 9400
    }
  ]
}
```

