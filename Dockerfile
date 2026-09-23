# syntax=docker/dockerfile:1
# -----------------------------------------------------------------------------
# Stage 1: Build static Go binaries
# -----------------------------------------------------------------------------
FROM golang:alpine AS builder
WORKDIR /workspace

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/dcgm-compute-agent ./cmd/dcgm-compute-agent
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/dcgm-control-service ./cmd/dcgm-control-service

# -----------------------------------------------------------------------------
# Target: dcgm-control-service
# Control node service managing OpenStack VM discovery and mTLS sync.
# -----------------------------------------------------------------------------
FROM alpine:latest AS control-service
RUN apk add --no-cache ca-certificates

COPY --from=builder /bin/dcgm-control-service /usr/local/bin/dcgm-control-service

EXPOSE 8443

ENTRYPOINT ["/usr/local/bin/dcgm-control-service"]

# -----------------------------------------------------------------------------
# Target: dcgm-compute-agent (default final stage)
# Compute node agent managing veth interfaces, OVS ports, and scraping GPU metrics.
# Requires iproute2 and openvswitch (ovs-vsctl) for network management.
# -----------------------------------------------------------------------------
FROM alpine:latest AS compute-agent
RUN apk add --no-cache iproute2 openvswitch ca-certificates

COPY --from=builder /bin/dcgm-compute-agent /usr/local/bin/dcgm-compute-agent

EXPOSE 9405

ENTRYPOINT ["/usr/local/bin/dcgm-compute-agent"]

