package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/internal/controlconfig"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/controller"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/logger"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/openstack"
	"github.com/gophercloud/gophercloud/v2"
)

func main() {
	cfg, err := controlconfig.Load(os.Args[1:])
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.InitLogger(cfg.LogFormat, cfg.LogLevel)
	slog.Info("Starting dcgm-control-service",
		"listen_addr", cfg.ListenAddr,
		"auth_url", cfg.AuthURL,
		"mock_mode", cfg.UseMock,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Initialize OpenStack Client
	var osClient openstack.OpenStackClient
	if cfg.UseMock {
		slog.Info("Running in Mock OpenStack mode")
		osClient = openstack.NewMockOpenStackClient()
	} else {
		if cfg.AuthURL == "" {
			slog.Error("OS_AUTH_URL is required when not in mock mode. Run with -use-mock for local testing.")
			os.Exit(1)
		}

		endpointOpts := gophercloud.EndpointOpts{
			Region: cfg.Region,
		}
		client, err := openstack.NewGophercloudClient(ctx, cfg.ToAuthOptions(), endpointOpts, cfg.ToVMFilterConfig())
		if err != nil {
			slog.Error("Failed connecting to OpenStack", "error", err)
			os.Exit(1)
		}
		osClient = client
	}

	// 2. Initialize HTTP Handler & Server
	handler := controller.NewHandler(osClient)
	srv, err := controller.NewServer(cfg.ListenAddr, handler, cfg.TLSCertPath, cfg.TLSKeyPath, cfg.ClientCACertPath)
	if err != nil {
		slog.Error("Failed creating HTTPS server", "error", err)
		os.Exit(1)
	}

	// 3. Start HTTPS Server
	go func() {
		if err := srv.Start(); err != nil {
			slog.Error("HTTPS server terminated", "error", err)
		}
	}()

	// 4. Wait for termination signals
	<-ctx.Done()
	stop()
	slog.Info("Termination signal received, shutting down gracefully...")

	// 5. Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Error during shutdown", "error", err)
	}

	slog.Info("dcgm-control-service exited cleanly")
}
