package controller

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/openstack"
)

func TestController_HandleSync_InsecureAndAuthorized(t *testing.T) {
	mockOS := openstack.NewMockOpenStackClient()
	mockOS.VMsByHost["hgx087"] = []api.TargetVM{
		{VMID: "vm-1", VMName: "ai-workload-1", GuestIP: "10.0.0.10", Port: 9400},
	}

	handler := NewHandler(mockOS)

	// 1. Without TLS and without allowInsecureTLS: must return 401
	req := httptest.NewRequest(http.MethodPost, "/api/v1/host/sync", bytes.NewReader([]byte(`{"action":"sync"}`)))
	w := httptest.NewRecorder()
	handler.HandleSync(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without cert, got %d", w.Code)
	}

	// 2. With allowInsecureTLS and X-Compute-Host header: must return 200 with data
	handler.SetAllowInsecureTLS(true)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/host/sync", bytes.NewReader([]byte(`{"action":"sync"}`)))
	req.Header.Set("X-Compute-Host", "hgx087")
	w = httptest.NewRecorder()
	handler.HandleSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp api.SyncResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response failed: %v", err)
	}
	if len(resp.Targets) != 1 || resp.Targets[0].VMID != "vm-1" {
		t.Errorf("unexpected targets: %+v", resp.Targets)
	}
}

func TestController_mTLSEndToEnd(t *testing.T) {
	// Generate in-memory CA and certificates
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test-CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA cert: %v", err)
	}
	caCert, _ := x509.ParseCertificate(caCertDER)

	// Generate Server Cert
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, _ := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	serverTLSCert := tls.Certificate{
		Certificate: [][]byte{serverDER},
		PrivateKey:  serverKey,
	}

	// Generate Client Cert with CN=hgx087
	clientKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "hgx087"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, _ := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)
	clientTLSCert := tls.Certificate{
		Certificate: [][]byte{clientDER},
		PrivateKey:  clientKey,
	}

	// Setup mock OpenStack with data for hgx087
	mockOS := openstack.NewMockOpenStackClient()
	mockOS.VMsByHost["hgx087"] = []api.TargetVM{
		{VMID: "vm-mtls", VMName: "mtls-worker", GuestIP: "10.0.0.99", Port: 9400},
	}
	handler := NewHandler(mockOS)

	// Create test TLS server requiring client cert
	clientCAPool := x509.NewCertPool()
	clientCAPool.AddCert(caCert)

	ts := httptest.NewUnstartedServer(http.HandlerFunc(handler.HandleSync))
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAPool,
	}
	ts.StartTLS()
	defer ts.Close()

	// 1. Client without certificate must fail TLS handshake
	badClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: clientCAPool,
			},
		},
	}
	_, err = badClient.Post(ts.URL, "application/json", bytes.NewReader([]byte(`{"action":"sync"}`)))
	if err == nil {
		t.Fatalf("expected request without client cert to fail TLS handshake")
	}

	// 2. Client with certificate CN=hgx087 must succeed
	goodClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      clientCAPool,
				Certificates: []tls.Certificate{clientTLSCert},
			},
		},
	}
	resp, err := goodClient.Post(ts.URL, "application/json", bytes.NewReader([]byte(`{"action":"sync"}`)))
	if err != nil {
		t.Fatalf("mTLS request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var syncResp api.SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&syncResp); err != nil {
		t.Fatalf("decoding response failed: %v", err)
	}
	if len(syncResp.Targets) != 1 || syncResp.Targets[0].VMID != "vm-mtls" {
		t.Errorf("expected target vm-mtls, got: %+v", syncResp.Targets)
	}
}
