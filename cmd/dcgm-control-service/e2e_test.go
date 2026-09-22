package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/controller"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/coordinator"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/openstack"
)

func TestControlService_EndToEnd_mTLSSync(t *testing.T) {
	// 1. Generate CA
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(100),
		Subject:               pkix.Name{CommonName: "Cluster-CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	caCert, _ := x509.ParseCertificate(caDER)

	// 2. Generate Server Certificate
	serverKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(101),
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

	// 3. Generate Compute Client Certificate (CN=compute-node-1)
	clientKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(102),
		Subject:      pkix.Name{CommonName: "compute-node-1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, _ := x509.CreateCertificate(rand.Reader, clientTemplate, caCert, &clientKey.PublicKey, caKey)

	// Write temp cert files for Coordinator
	tmpDir, err := os.MkdirTemp("", "mtls-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	caFile := tmpDir + "/ca.crt"
	clientCertFile := tmpDir + "/client.crt"
	clientKeyFile := tmpDir + "/client.key"

	writePEM(caFile, "CERTIFICATE", caDER)
	writePEM(clientCertFile, "CERTIFICATE", clientDER)
	writePEM(clientKeyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(clientKey))

	// 4. Setup mock OpenStack
	mockOS := openstack.NewMockOpenStackClient()
	mockOS.VMsByHost["compute-node-1"] = []api.TargetVM{
		{VMID: "vm-gpu-100", VMName: "ai-worker", GuestIP: "10.0.0.20", Port: 9400},
	}

	// 5. Start TLS test server
	handler := controller.NewHandler(mockOS)
	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	ts := httptest.NewUnstartedServer(http.HandlerFunc(handler.HandleSync))
	ts.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
	}
	ts.StartTLS()
	defer ts.Close()

	// 6. Run Compute Agent Coordinator using real mTLS files
	coordCfg := coordinator.Config{
		ControllerURL: ts.URL,
		HostName:      "compute-node-1",
		CACertPath:    caFile,
		CertPath:      clientCertFile,
		KeyPath:       clientKeyFile,
		SyncTimeout:   2 * time.Second,
	}

	coord, err := coordinator.NewCoordinator(coordCfg, nil)
	if err != nil {
		t.Fatalf("failed to initialize coordinator: %v", err)
	}

	// Perform sync
	if err := coord.SyncOnce(context.Background()); err != nil {
		t.Fatalf("mTLS sync failed: %v", err)
	}

	targets := coord.GetTargets()
	if len(targets) != 1 || targets[0].VMID != "vm-gpu-100" {
		t.Fatalf("unexpected targets returned via mTLS: %+v", targets)
	}
}

func writePEM(path, blockType string, data []byte) {
	f, _ := os.Create(path)
	defer f.Close()
	_ = pem.Encode(f, &pem.Block{Type: blockType, Bytes: data})
}
