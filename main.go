// Command kubernetes-mcp is an MCP (Model Context Protocol) server that lets an
// AI agent manage one or more Kubernetes clusters. It authenticates to every
// cluster with a Kubernetes ServiceAccount token and relies entirely on
// Kubernetes RBAC for authorization — it performs no auth of its own.
//
// The HTTP (streamable) transport has NO built-in authentication. Run it inside
// a trusted network and place it behind an internal ingress (e.g. WireGuard /
// basic-auth). Never expose it publicly.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/auth"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/clusters"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/config"
	"github.com/truepace-io-oss/kubernetes-mcp-server/internal/mcpserver"
)

// version is set via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", os.Getenv("KMCP_CONFIG"), "path to the server config YAML (env: KMCP_CONFIG)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if err := run(*configPath); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	mcpserver.SetVersion(version)

	for _, w := range cfg.Warnings() {
		logger.Warn("config", "warning", w)
	}

	reg, err := clusters.Build(cfg)
	if err != nil {
		return err
	}
	logger.Info("cluster registry built", "clusters", cfg.ClusterNames(), "default", cfg.DefaultCluster, "readOnly", cfg.ReadOnly)

	srv := mcpserver.New(reg, cfg)

	// Agent-facing authentication (independent of cluster auth).
	authn, err := auth.Build(context.Background(), cfg.Auth)
	if err != nil {
		return err
	}
	logger.Info("agent authentication", "mode", authn.Description)

	// One MCP server value, reused for every session.
	mcpSrv := srv.MCPServer()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)

	mux := http.NewServeMux()
	// Only /mcp is authenticated; health probes stay open, and the
	// protected-resource metadata endpoint is public by spec.
	mux.Handle("/mcp", authn.Middleware(handler))
	if authn.MetadataHandler != nil {
		mux.Handle(authn.MetadataPath, authn.MetadataHandler)
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// Ready if the default cluster is reachable. A degraded remote cluster
		// must not fail readiness, so only the default is probed.
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if _, err := reg.Default().Ping(ctx); err != nil {
			http.Error(w, "default cluster unreachable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.ListenAddr, "endpoint", "/mcp")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))
}
