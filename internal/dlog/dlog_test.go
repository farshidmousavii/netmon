package dlog

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// decodeJSONLine parses one JSON log line into a map.
func decodeJSONLine(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("line is not valid JSON: %v\nline: %s", err, line)
	}
	return m
}

// TestJSONLinesAtRightLevels: every level emits exactly one valid JSON line
// with the expected level, message, and structured attributes (no string
// concatenation anywhere).
func TestJSONLinesAtRightLevels(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Options{Level: slog.LevelDebug, Out: &buf})

	logger.Debug("debug message", "device_id", 7)
	logger.Info("info message", "target", "10.0.0.1")
	logger.Warn("warn message", "attempt", 3)
	logger.Error("error message", "err", "boom")

	lines := bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n"))
	if len(lines) != 4 {
		t.Fatalf("got %d log lines, want 4: %q", len(lines), buf.String())
	}

	want := []struct {
		level string
		msg   string
	}{
		{"DEBUG", "debug message"},
		{"INFO", "info message"},
		{"WARN", "warn message"},
		{"ERROR", "error message"},
	}
	for i, w := range want {
		m := decodeJSONLine(t, lines[i])
		if m["level"] != w.level {
			t.Errorf("line %d: level = %v, want %s", i, m["level"], w.level)
		}
		if m["msg"] != w.msg {
			t.Errorf("line %d: msg = %v, want %s", i, m["msg"], w.msg)
		}
		if _, ok := m["time"]; !ok {
			t.Errorf("line %d: missing time field", i)
		}
	}

	// Structured attributes survive into the JSON (no string formatting).
	if m := decodeJSONLine(t, lines[0]); m["device_id"] != float64(7) {
		t.Errorf("debug line: device_id = %v, want 7", m["device_id"])
	}
	if m := decodeJSONLine(t, lines[1]); m["target"] != "10.0.0.1" {
		t.Errorf("info line: target = %v, want 10.0.0.1", m["target"])
	}
}

// TestLevelFiltering: messages below the configured level are dropped.
func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(Options{Level: slog.LevelInfo, Out: &buf}) // default level anyway

	logger.Debug("should be dropped")
	logger.Info("should be kept")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (debug filtered at info level): %q", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], `"msg":"should be kept"`) {
		t.Errorf("unexpected line: %s", lines[0])
	}
}

// TestNewDefaultOptions: nil Out falls back to stdout and zero level means
// info; the logger constructs and discards without error (smoke).
func TestNewDefaultOptions(t *testing.T) {
	logger := New(Options{})
	if logger == nil {
		t.Fatal("New returned nil logger")
	}
	// Construct-and-discard: just proves the default path works.
	logger.Info("smoke")
}

func TestNewFromEnv(t *testing.T) {
	t.Run("unset defaults to info", func(t *testing.T) {
		t.Setenv(LevelEnv, "")
		logger, err := NewFromEnv()
		if err != nil {
			t.Fatalf("NewFromEnv: %v", err)
		}
		if !logger.Enabled(nil, slog.LevelInfo) {
			t.Error("info should be enabled at default level")
		}
		if logger.Enabled(nil, slog.LevelDebug) {
			t.Error("debug should be disabled at default info level")
		}
	})

	for _, raw := range []string{"debug", "DEBUG", "info", "warn", "error"} {
		t.Run("level "+raw, func(t *testing.T) {
			t.Setenv(LevelEnv, raw)
			if _, err := NewFromEnv(); err != nil {
				t.Errorf("NewFromEnv(%q): %v", raw, err)
			}
		})
	}

	t.Run("invalid level rejected", func(t *testing.T) {
		t.Setenv(LevelEnv, "chatty")
		_, err := NewFromEnv()
		if err == nil {
			t.Fatal("expected error for invalid level")
		}
		if !strings.Contains(err.Error(), LevelEnv) {
			t.Errorf("error should mention %s, got: %v", LevelEnv, err)
		}
	})
}

func TestLevelFromEnv(t *testing.T) {
	t.Setenv(LevelEnv, "warn")
	level, err := LevelFromEnv()
	if err != nil {
		t.Fatalf("LevelFromEnv: %v", err)
	}
	if level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", level)
	}
}
