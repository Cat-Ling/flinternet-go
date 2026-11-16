package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/flinternet/flinternet/internal/pinterest"
	"github.com/flinternet/flinternet/internal/server"
	"github.com/flinternet/flinternet/web"
)

func main() {
	var (
		addr                      = flag.String("addr", defaultListenAddr(":8080"), "HTTP listen address (env: FLINTERNET_ADDR or PORT)")
		baseURL                   = flag.String("base-url", envOrDefault("FLINTERNET_BASE_URL", pinterest.DefaultBaseURL), "Pinterest HTTPS web host (env: FLINTERNET_BASE_URL)")
		timeout                   = flag.Duration("timeout", envDurationOrDefault("FLINTERNET_TIMEOUT", 20*time.Second), "Upstream request timeout (env: FLINTERNET_TIMEOUT)")
		cacheTTL                  = flag.Duration("cache-ttl", envDurationOrDefault("FLINTERNET_CACHE_TTL", 5*time.Minute), "In-memory response cache TTL (env: FLINTERNET_CACHE_TTL)")
		appVersion                = flag.String("app-version", envOrDefault("FLINTERNET_APP_VERSION", ""), "Optional X-App-Version header (env: FLINTERNET_APP_VERSION)")
		allowUnverifiedPagination = flag.Bool("allow-unverified-pagination", envBoolOrDefault("FLINTERNET_ALLOW_UNVERIFIED_PAGINATION", true), "Allow best-effort unverified search/feed cursor continuations (env: FLINTERNET_ALLOW_UNVERIFIED_PAGINATION)")
		shareKey                  = flag.String("share-key", envOrDefault("FLINTERNET_SHARE_KEY", ""), "Secret key enabling privacy-preserving encrypted pin correlation tokens (env: FLINTERNET_SHARE_KEY)")
		logLevel                  = flag.String("log-level", envOrDefault("FLINTERNET_LOG_LEVEL", "info"), "Log level: debug, info, warn, error (env: FLINTERNET_LOG_LEVEL)")
	)
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(*logLevel)}))

	client, err := pinterest.NewClient(pinterest.Config{
		BaseURL:                   *baseURL,
		Timeout:                   *timeout,
		AppVersion:                *appVersion,
		AllowUnverifiedPagination: *allowUnverifiedPagination,
	})
	if err != nil {
		logger.Error("failed to initialize pinterest client", "error", err)
		os.Exit(1)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		logger.Error("failed to load embedded static assets", "error", err)
		os.Exit(1)
	}
	templatesFS, err := web.TemplatesFS()
	if err != nil {
		logger.Error("failed to load embedded HTML templates", "error", err)
		os.Exit(1)
	}

	srv, err := server.New(server.Config{
		Client:      client,
		StaticFS:    staticFS,
		TemplatesFS: templatesFS,
		CacheTTL:    *cacheTTL,
		ShareKey:    *shareKey,
		Logger:      logger,
	})
	if err != nil {
		logger.Error("failed to initialize HTTP server", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("flinternet server listening", "version", server.Version, "addr", *addr, "base_url", *baseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	logger.Info("shutting down gracefully")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
}

func defaultListenAddr(fallback string) string {
	if addr := strings.TrimSpace(os.Getenv("FLINTERNET_ADDR")); addr != "" {
		return addr
	}
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		if strings.HasPrefix(port, ":") {
			return port
		}
		return ":" + port
	}
	return fallback
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func envBoolOrDefault(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return b
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
