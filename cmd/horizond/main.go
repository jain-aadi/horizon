package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jain-aadi/horizon/internal/api"
	"github.com/jain-aadi/horizon/internal/config"
	"github.com/jain-aadi/horizon/internal/sandbox"
	"github.com/jain-aadi/horizon/internal/storage"
)

// loadEnvFile loads simple KEY=VALUE entries from a .env file without
// overriding values inherited from the process environment.
func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // .env file is optional
		}
		return err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// The inherited process environment takes precedence, including an
		// explicitly empty value.
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, value)
		}
	}
	return nil
}

func applyFlagOverrides(cfg *config.Config, flags *flag.FlagSet, dbPath string, port int) {
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "db":
			cfg.Database.Path = dbPath
		case "port":
			cfg.Server.Port = port
		}
	})
}

func main() {
	var (
		dbPath     = flag.String("db", "./data/horizon.db", "Database path")
		port       = flag.Int("port", 8080, "HTTP server port")
		healthOnly = flag.Bool("health-check", false, "Run health check and exit")
	)
	flag.Parse()

	// Load .env file
	if err := loadEnvFile(".env"); err != nil {
		log.Printf("Warning: failed to load .env: %v", err)
	}

	cfg := config.DefaultConfig()
	if err := cfg.LoadFromEnv(); err != nil {
		log.Fatalf("Failed to load environment configuration: %v", err)
	}
	applyFlagOverrides(cfg, flag.CommandLine, *dbPath, *port)

	// Initialize database
	db, err := storage.NewDB(cfg.Database.Path)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Initialize Podman engine
	podmanConfig := sandbox.EngineConfig{
		SocketPath:       cfg.Podman.SocketPath,
		ImageName:        cfg.Podman.ImageName,
		APIVersion:       cfg.Podman.APIVersion,
		RequestTimeout:   cfg.Podman.RequestTimeout,
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
		DataDir:        filepath.Dir(cfg.Database.Path),
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
