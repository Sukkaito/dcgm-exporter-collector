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
	slog.Debug("Configuration loaded successfully",
		"listen_addr", cfg.ListenAddr,
		"auth_url", cfg.AuthURL,
		"region", cfg.Region,
		"mock_mode", cfg.UseMock,
		"tls_cert", cfg.TLSCertPath,
		"tls_key", cfg.TLSKeyPath,
		"client_ca", cfg.ClientCACertPath,
	)

	slog.Info("Starting dcgm-control-service",
		"listen_addr", cfg.ListenAddr,
		"auth_url", cfg.AuthURL,
		"mock_mode", cfg.UseMock,
	)

	slog.Debug("Registering signal handlers for OS termination signals")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Initialize OpenStack Client
	var osClient openstack.OpenStackClient
	if cfg.UseMock {
		slog.Info("Running in Mock OpenStack mode")
		slog.Debug("Instantiating mock OpenStack client")
		osClient = openstack.NewMockOpenStackClient()
	} else {
		if cfg.AuthURL == "" {
			slog.Error("OS_AUTH_URL is required when not in mock mode. Run with -use-mock for local testing.")
			os.Exit(1)
		}

		slog.Debug("Initializing Gophercloud OpenStack client", "auth_url", cfg.AuthURL, "region", cfg.Region)
		endpointOpts := gophercloud.EndpointOpts{
			Region: cfg.Region,
		}
		client, err := openstack.NewGophercloudClient(ctx, cfg.ToAuthOptions(), endpointOpts, cfg.ToVMFilterConfig())
		if err != nil {
			slog.Error("Failed connecting to OpenStack", "error", err)
			os.Exit(1)
		}
		osClient = client
		slog.Debug("OpenStack client initialized successfully")
	}

	// 2. Initialize HTTP Handler & Server
	slog.Debug("Initializing HTTP handler and HTTPS server", "listen_addr", cfg.ListenAddr)
	handler := controller.NewHandler(osClient)
	srv, err := controller.NewServer(cfg.ListenAddr, handler, cfg.TLSCertPath, cfg.TLSKeyPath, cfg.ClientCACertPath)
	if err != nil {
		slog.Error("Failed creating HTTPS server", "error", err)
		os.Exit(1)
	}
	slog.Debug("HTTPS server created successfully")

	// 3. Start HTTPS Server
	slog.Debug("Launching HTTPS server in background goroutine")
	go func() {
		if err := srv.Start(); err != nil {
			slog.Error("HTTPS server terminated", "error", err)
		}
	}()

	// 4. Wait for termination signals
	slog.Debug("Waiting for shutdown signal or context cancellation")
	<-ctx.Done()
	stop()
	slog.Info("Termination signal received, shutting down gracefully...")

	// 5. Graceful shutdown
	slog.Debug("Initiating server shutdown with timeout", "timeout_seconds", 10)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Error during shutdown", "error", err)
	} else {
		slog.Debug("Server shutdown completed without error")
	}

	slog.Info("dcgm-control-service exited cleanly")
}
