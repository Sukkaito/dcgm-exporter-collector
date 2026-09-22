package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/internal/config"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/coordinator"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/logger"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/network"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/processor"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/scraper"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/server"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/transport"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	logger.InitLogger(cfg.LogFormat, cfg.LogLevel)
	slog.Info("Starting dcgm-compute-agent",
		"hostname", cfg.HostName,
		"controller_url", cfg.ControllerURL,
		"listen_addr", cfg.ListenAddr,
		"max_workers", cfg.MaxScrapeWorkers,
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 1. Initialize Network Provisioner subsystem
	cmdRunner := network.NewExecCommandRunner(10 * time.Second)
	vethMgr := network.NewVethManager(cmdRunner)
	ovsMgr := network.NewOVSManager(cmdRunner, cfg.OVSVsctlPath, cfg.OVSBridge)
	netMgr := network.NewNetworkManager(vethMgr, ovsMgr, cfg.OVSBridge)

	// 2. Initialize In-Memory Coordinator & Control-Node Client
	coordCfg := coordinator.Config{
		ControllerURL: cfg.ControllerURL,
		HostName:      cfg.HostName,
		CACertPath:    cfg.CACertPath,
		CertPath:      cfg.CertPath,
		KeyPath:       cfg.KeyPath,
		SyncInterval:  cfg.SyncInterval,
		SyncTimeout:   cfg.SyncTimeout,
		StaticTargets: cfg.StaticTargets,
	}
	coord, err := coordinator.NewCoordinator(coordCfg, netMgr)
	if err != nil {
		slog.Error("Failed initializing coordinator", "error", err)
		os.Exit(1)
	}

	// 3. Initialize Telemetry Collector subsystem
	tr := transport.NewHTTPTransport(cfg.ScrapeTimeout, 10*1024*1024)
	sc := scraper.NewScraper(tr, cfg.ScrapeTimeout, cfg.MaxScrapeWorkers)
	enricher := processor.NewMetricEnricher(cfg.HostName)
	srv := server.NewServer(cfg.ListenAddr, coord, sc, enricher, cfg.CacheTTL)

	// 4. Start background coordinator sync loop
	go coord.Start(ctx)

	// 5. Start HTTP telemetry server
	go func() {
		if err := srv.Start(); err != nil {
			slog.Error("HTTP telemetry server terminated", "error", err)
		}
	}()

	// 6. Wait for termination signals
	<-ctx.Done()
	stop()
	slog.Info("Termination signal received, shutting down gracefully...")

	// 7. Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Error during HTTP server shutdown", "error", err)
	}

	slog.Info("dcgm-compute-agent exited cleanly")
}
