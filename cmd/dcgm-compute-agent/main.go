package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/internal/config"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/coordinator"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/network"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/processor"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/scraper"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/server"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/transport"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.Println("[main] Starting dcgm-compute-agent...")

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("[main] Error loading configuration: %v", err)
	}

	log.Printf("[main] Hostname: %s, Controller URL: %s, Listen Addr: %s",
		cfg.HostName, cfg.ControllerURL, cfg.ListenAddr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		log.Fatalf("[main] Error initializing coordinator: %v", err)
	}

	// 3. Initialize Telemetry Collector subsystem
	tr := transport.NewHTTPTransport(cfg.ScrapeTimeout, 10*1024*1024)
	sc := scraper.NewScraper(tr, cfg.ScrapeTimeout)
	enricher := processor.NewMetricEnricher(cfg.HostName)
	srv := server.NewServer(cfg.ListenAddr, coord, sc, enricher, cfg.CacheTTL)

	// 4. Start background coordinator sync loop
	go coord.Start(ctx)

	// 5. Start HTTP telemetry server
	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("[main] HTTP server error: %v", err)
		}
	}()

	// 6. Wait for termination signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sig := <-sigCh
	log.Printf("[main] Received signal %v, shutting down gracefully...", sig)

	// 7. Graceful shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] Error during HTTP server shutdown: %v", err)
	}

	log.Println("[main] dcgm-compute-agent exited cleanly")
}
