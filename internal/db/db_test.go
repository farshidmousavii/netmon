package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDatabaseURLFromEnv(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv(DatabaseURLEnv, "")
		url, err := DatabaseURLFromEnv()
		if err == nil {
			t.Fatalf("expected error when %s unset, got url %q", DatabaseURLEnv, url)
		}
		if !strings.Contains(err.Error(), DatabaseURLEnv) {
			t.Errorf("error should mention %s, got: %v", DatabaseURLEnv, err)
		}
	})

	t.Run("set", func(t *testing.T) {
		t.Setenv(DatabaseURLEnv, "postgres://user:pass@localhost:5432/bidar")
		url, err := DatabaseURLFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if url != "postgres://user:pass@localhost:5432/bidar" {
			t.Errorf("got url %q", url)
		}
	})
}

func TestOpenRejectsEmptyURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := Open(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty database URL")
	}
	if pool != nil {
		t.Fatalf("expected nil pool on error, got %v", pool)
	}
}

func TestOpenFailsFastOnUnreachableDB(t *testing.T) {
	// Port 1 (tcpmux) is closed on virtually every host: dial fails
	// immediately, so this does not wait out the 10s ping timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := Open(ctx, "postgres://user:pass@127.0.0.1:1/bidar")
	if err == nil {
		t.Fatal("expected error for unreachable database")
	}
	if pool != nil {
		t.Fatalf("expected nil pool on error, got %v", pool)
	}
	if !strings.Contains(err.Error(), "ping database") {
		t.Errorf("error should wrap the ping failure, got: %v", err)
	}
}

func TestMigrateRejectsEmptyURL(t *testing.T) {
	if err := Migrate(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty database URL")
	}
}
