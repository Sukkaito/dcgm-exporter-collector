package controller

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/openstack"
)

// Handler handles control node synchronization and health endpoints.
type Handler struct {
	osClient         openstack.OpenStackClient
	allowInsecureTLS bool // for local unit tests without TLS
}

// NewHandler creates a new Handler.
func NewHandler(osClient openstack.OpenStackClient) *Handler {
	return &Handler{osClient: osClient}
}

// SetAllowInsecureTLS enables or disables bypassing client certificate requirement (for local tests).
func (h *Handler) SetAllowInsecureTLS(allow bool) {
	h.allowInsecureTLS = allow
}

// HandleSync processes compute node synchronization requests.
func (h *Handler) HandleSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	computeHost, err := h.extractComputeHost(r)
	if err != nil {
		log.Printf("[controller] Unauthorized sync attempt: %v", err)
		http.Error(w, fmt.Sprintf("Unauthorized: %v", err), http.StatusUnauthorized)
		return
	}

	log.Printf("[controller] Handling sync request for authenticated host: %s", computeHost)

	// 1. Discover GPU passthrough VMs for the compute host
	targets, err := h.osClient.DiscoverComputeVMs(r.Context(), computeHost)
	if err != nil {
		log.Printf("[controller] Error discovering VMs on %s: %v", computeHost, err)
		http.Error(w, fmt.Sprintf("Failed to discover VMs: %v", err), http.StatusInternalServerError)
		return
	}

	// 2. Collect unique network IDs across discovered VMs
	networkSet := make(map[string]struct{})
	for _, t := range targets {
		_ = t
	}

	// 3. Ensure persistent host ports for all required networks
	var networkList []string
	for netID := range networkSet {
		networkList = append(networkList, netID)
	}

	endpoints, err := h.osClient.EnsureHostPorts(r.Context(), computeHost, networkList)
	if err != nil {
		log.Printf("[controller] Error ensuring host ports for %s: %v", computeHost, err)
		http.Error(w, fmt.Sprintf("Failed to ensure host ports: %v", err), http.StatusInternalServerError)
		return
	}

	resp := api.SyncResponse{
		Status:    "ok",
		Endpoints: endpoints,
		Targets:   targets,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("[controller] Error encoding sync response: %v", err)
	}
}

// HandleHealthz responds with liveness status.
func (h *Handler) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

// HandleReadyz responds with readiness status by verifying OpenStack connectivity.
func (h *Handler) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.osClient.HealthCheck(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf("Not ready: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY\n"))
}

func (h *Handler) extractComputeHost(r *http.Request) (string, error) {
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		cert := r.TLS.PeerCertificates[0]
		return ExtractHostFromCert(cert)
	}

	if h.allowInsecureTLS {
		// Insecure fallback: check X-Compute-Host header or request body
		if host := r.Header.Get("X-Compute-Host"); host != "" {
			return host, nil
		}
		var req api.SyncRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Hostname != "" {
			return req.Hostname, nil
		}
		return "insecure-compute-host", nil
	}

	return "", fmt.Errorf("no valid client certificate presented in TLS handshake")
}

// ExtractHostFromCert extracts the compute hostname from client certificate CN or SAN.
func ExtractHostFromCert(cert *x509.Certificate) (string, error) {
	if cert.Subject.CommonName != "" {
		return strings.TrimSpace(cert.Subject.CommonName), nil
	}
	if len(cert.DNSNames) > 0 {
		return strings.TrimSpace(cert.DNSNames[0]), nil
	}
	return "", fmt.Errorf("certificate does not contain CommonName or DNS SAN")
}
