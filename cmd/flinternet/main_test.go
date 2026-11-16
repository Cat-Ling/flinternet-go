package main

import (
	"log/slog"
	"testing"
	"time"
)

func TestEnvHelpers(t *testing.T) {
	t.Setenv("FLINTERNET_ADDR", "")
	t.Setenv("PORT", "9090")
	if got := defaultListenAddr(":8080"); got != ":9090" {
		t.Fatalf("expected :9090 from PORT fallback, got %q", got)
	}

	t.Setenv("FLINTERNET_ADDR", "127.0.0.1:7777")
	if got := defaultListenAddr(":8080"); got != "127.0.0.1:7777" {
		t.Fatalf("expected FLINTERNET_ADDR to override PORT, got %q", got)
	}

	t.Setenv("FLINTERNET_TIMEOUT", "45s")
	if got := envDurationOrDefault("FLINTERNET_TIMEOUT", 20*time.Second); got != 45*time.Second {
		t.Fatalf("expected 45s duration, got %v", got)
	}
	t.Setenv("FLINTERNET_TIMEOUT", "not-a-duration")
	if got := envDurationOrDefault("FLINTERNET_TIMEOUT", 20*time.Second); got != 20*time.Second {
		t.Fatalf("expected fallback 20s for invalid duration, got %v", got)
	}

	t.Setenv("FLINTERNET_ALLOW_UNVERIFIED_PAGINATION", "false")
	if envBoolOrDefault("FLINTERNET_ALLOW_UNVERIFIED_PAGINATION", true) {
		t.Fatalf("expected false from envBoolOrDefault")
	}
	t.Setenv("FLINTERNET_ALLOW_UNVERIFIED_PAGINATION", "invalid")
	if !envBoolOrDefault("FLINTERNET_ALLOW_UNVERIFIED_PAGINATION", true) {
		t.Fatalf("expected fallback true for invalid bool")
	}

	if parseLogLevel("DEBUG") != slog.LevelDebug || parseLogLevel("warn") != slog.LevelWarn || parseLogLevel("error") != slog.LevelError || parseLogLevel("unknown") != slog.LevelInfo {
		t.Fatalf("unexpected log level mapping")
	}

	t.Setenv("FLINTERNET_SHARE_KEY", "  my-secret-key  ")
	if got := envOrDefault("FLINTERNET_SHARE_KEY", ""); got != "my-secret-key" {
		t.Fatalf("expected trimmed FLINTERNET_SHARE_KEY, got %q", got)
	}
}
