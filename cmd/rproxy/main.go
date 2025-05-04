package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"rproxy/internal/certs"
	"rproxy/internal/config"
	"rproxy/internal/podman"
	"rproxy/internal/proxy"
	"rproxy/internal/sshclient"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

func main() {
	// Configure slog
	logHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339))
			}
			if a.Key == slog.SourceKey {
				source, _ := a.Value.Any().(*slog.Source)
				if source != nil {
					a.Value = slog.StringValue(fmt.Sprintf("%s:%d", filepath.Base(source.File), source.Line))
				}
			}
			return a
		},
	})
	logger := slog.New(logHandler)
	slog.SetDefault(logger)

	// Redirect standard log output to slog
	log.SetOutput(slog.NewLogLogger(logHandler, slog.LevelInfo).Writer())
	log.SetFlags(0) // Disable standard log flags (like date/time/file)

	slog.Info("Starting rproxy...")

	// 1. Load Configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// 2. Initialize SSH Client
	sshClient, err := sshclient.New(cfg.SSHUser, cfg.SSHHost, cfg.SSHPort)
	if err != nil {
		slog.Error("Failed to create SSH client", "error", err)
		os.Exit(1)
	}

	// 3. Initialize Podman Client
	podmanClient := podman.New(sshClient)

	// 4. Initialize Certificate Manager
	certManager, err := certs.NewManager(cfg)
	if err != nil {
		slog.Error("Failed to create certificate manager", "error", err)
		os.Exit(1)
	}

	// 5. Initialize Router
	router := proxy.NewRouter(cfg, podmanClient, certManager)

	// 6. Initialize Proxy Server
	proxyServer := proxy.NewServer(router, certManager)

	// --- Setup graceful shutdown --- 
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Use errgroup to manage goroutines and propagate errors
	var eg errgroup.Group

	// --- Start components --- 

	// Start Router Update Loop
	eg.Go(func() error {
		router.RunUpdateLoop(ctx)
		return nil
	})

	// Start Proxy Server
	eg.Go(func() error {
		if err := proxyServer.Start(ctx); err != nil {
			slog.Error("Proxy server failed", "error", err)
			return err
		}
		slog.Info("Proxy server finished gracefully.")
		return nil
	})

	// --- Wait for shutdown or error --- 
	slog.Info("rproxy running. Press Ctrl+C to shut down.")
	if err := eg.Wait(); err != nil {
		slog.Error("Shutting down due to error", "error", err)
		os.Exit(1)
	}

	slog.Info("rproxy shut down gracefully.")
} 