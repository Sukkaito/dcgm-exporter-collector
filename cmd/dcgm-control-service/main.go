package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/internal/controlconfig"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/controller"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/openstack"
	"github.com/gophercloud/gophercloud/v2"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.Println("[main] Starting dcgm-control-service...")

	cfg, err := controlconfig.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("[main] Error loading configuration: %v", err)
	}

	log.Printf("[main] Listen Addr: %s, Auth URL: %s, Mock Mode: %v",
		cfg.ListenAddr, cfg.AuthURL, cfg.UseMock)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize OpenStack Client
	var osClient openstack.OpenStackClient
	if cfg.UseMock {
		log.Println("[main] Running in Mock OpenStack mode")
		osClient = openstack.NewMockOpenStackClient()
	} else {
		if cfg.AuthURL == "" {
			log.Fatalf("[main] OS_AUTH_URL is required when not in mock mode. Run with -use-mock for local testing.")
		}

		endpointOpts := gophercloud.EndpointOpts{
			Region: cfg.Region,
		}
		client, err := openstack.NewGophercloudClient(ctx, cfg.ToAuthOptions(), endpointOpts, cfg.ToVMFilterConfig())
		if err != nil {
			log.Fatalf("[main] Error connecting to OpenStack: %v", err)
		}
		osClient = client
	}

	// 2. Initialize HTTP Handler & Server
	handler := controller.NewHandler(osClient)
	srv, err := controller.NewServer(cfg.ListenAddr, handler, cfg.TLSCertPath, cfg.TLSKeyPath, cfg.ClientCACertPath)
	if err != nil {
		log.Fatalf("[main] Error creating HTTPS server: %v", err)
	}

	// 3. Start HTTPS Server
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("[main] HTTPS server failed: %v", err)
		}
	}()

	// 4. Handle termination signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("[main] Received signal %v, shutting down gracefully...", sig)

	// 5. Graceful shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] Error during shutdown: %v", err)
	}

	log.Println("[main] dcgm-control-service exited cleanly")
}
