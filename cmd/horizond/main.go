package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/horizon/horizon/internal/api"
	"github.com/horizon/horizon/internal/config"
	"github.com/horizon/horizon/internal/sandbox"
	"github.com/horizon/horizon/internal/storage"
)

func main() {
	var (
		configPath = flag.String("config", "", "Path to config file (optional)")
		dbPath     = flag.String("db", "./data/horizon.db", "Database path")
		port       = flag.Int("port", 8080, "HTTP server port")
		healthOnly = flag.Bool("health-check", false, "Run health check and exit")
	)
	flag.Parse()

	cfg := config.DefaultConfig()
	if *configPath != "" {
		// TODO: Load config from file
	}
	cfg.Database.Path = *dbPath
	cfg.Server.Port = *port

	// Initialize database
	db, err := storage.NewDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize Podman engine
	podmanConfig := sandbox.EngineConfig{
		SocketPath:      cfg.Podman.SocketPath,
		ImageName:       cfg.Podman.ImageName,
		APIVersion:      cfg.Podman.APIVersion,
		RequestTimeout:  cfg.Podman.RequestTimeout,
		ContainerTimeout: cfg.Podman.ContainerTimeout,
	}
	if podmanConfig.SocketPath == "" {
		podmanConfig = sandbox.DefaultEngineConfig()
		podmanConfig.ImageName = cfg.Podman.ImageName
		podmanConfig.APIVersion = cfg.Podman.APIVersion
		podmanConfig.RequestTimeout = cfg.Podman.RequestTimeout
		podmanConfig.ContainerTimeout = cfg.Podman.ContainerTimeout
	}

	engine, err := sandbox.NewEngine(podmanConfig)
	if err != nil {
		log.Fatalf("Failed to initialize Podman engine: %v", err)
	}

	// Health check mode
	if *healthOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := engine.HealthCheck(ctx); err != nil {
			log.Fatalf("Health check failed: %v", err)
		}
		fmt.Println("Health check passed")
		os.Exit(0)
	}

	// Create repositories
	agentRepo := storage.NewAgentRepository(db)
	runRepo := storage.NewRunRepository(db)

	// Create HTTP handler
	handler := api.NewHandler(agentRepo, runRepo, engine, &api.Config{
		DefaultTimeout: cfg.Agent.DefaultTimeout,
		DefaultImage:   cfg.Agent.DefaultImage,
	})

	// Setup HTTP server
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: mux,
	}

	// Start server
	go func() {
		log.Printf("Starting Horizon server on %s:%d", cfg.Server.Host, cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Server stopped")
}