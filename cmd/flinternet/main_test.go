package main

import (
	"log/slog"
	"os"
	"path/filepath"
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

func TestLoadDotEnvFile(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	content := "\ufeff# Comment line\n" +
		"FLINTERNET_TEST_A=hello # inline comment\n" +
		"export FLINTERNET_TEST_B=\"quoted value\\nline2\"\n" +
		"FLINTERNET_TEST_C='single # literal'\n" +
		"FLINTERNET_TEST_D=already_overridden\n" +
		"FLINTERNET_TEST_EMPTY=\n"
	if err := os.WriteFile(envPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	// Simulate existing process env precedence + cleanup for new vars set from .env.
	t.Setenv("FLINTERNET_TEST_D", "from_real_env")
	for _, k := range []string{"FLINTERNET_TEST_A", "FLINTERNET_TEST_B", "FLINTERNET_TEST_C", "FLINTERNET_TEST_EMPTY"} {
		_ = os.Unsetenv(k)
		k := k
		t.Cleanup(func() { _ = os.Unsetenv(k) })
	}

	if err := loadDotEnvFile(envPath); err != nil {
		t.Fatalf("loadDotEnvFile failed: %v", err)
	}

	if got := os.Getenv("FLINTERNET_TEST_A"); got != "hello" {
		t.Fatalf("FLINTERNET_TEST_A = %q, want %q", got, "hello")
	}
	if got := os.Getenv("FLINTERNET_TEST_B"); got != "quoted value\nline2" {
		t.Fatalf("FLINTERNET_TEST_B = %q, want multiline value", got)
	}
	if got := os.Getenv("FLINTERNET_TEST_C"); got != "single # literal" {
		t.Fatalf("FLINTERNET_TEST_C = %q, want %q", got, "single # literal")
	}
	if got := os.Getenv("FLINTERNET_TEST_D"); got != "from_real_env" {
		t.Fatalf("expected real env var to take precedence over .env, got %q", got)
	}
	if v, ok := os.LookupEnv("FLINTERNET_TEST_EMPTY"); !ok || v != "" {
		t.Fatalf("expected FLINTERNET_TEST_EMPTY to be present and empty, got %q ok=%v", v, ok)
	}
}
