package config

import (
	"errors"
	"flag"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		args        []string
		wantStorage string
		wantErr     bool
	}{
		{name: "defaults", wantStorage: "memory"},
		{name: "postgres", env: map[string]string{"STORAGE": "postgres", "DATABASE_URL": "postgres://localhost/test"}, wantStorage: "postgres"},
		{name: "flag overrides environment", env: map[string]string{"STORAGE": "postgres"}, args: []string{"-storage=memory"}, wantStorage: "memory"},
		{name: "missing database URL", args: []string{"-storage=postgres"}, wantErr: true},
		{name: "invalid storage", args: []string{"-storage=redis"}, wantErr: true},
		{name: "invalid timeout", env: map[string]string{"HTTP_READ_TIMEOUT": "bad"}, wantErr: true},
		{name: "zero timeout", env: map[string]string{"HTTP_WRITE_TIMEOUT": "0s"}, wantErr: true},
		{name: "negative limit", env: map[string]string{"HTTP_MAX_BODY_BYTES": "-1"}, wantErr: true},
		{name: "connection overflow", args: []string{"-storage=postgres"}, env: map[string]string{"DATABASE_MAX_CONNS": "2147483648"}, wantErr: true},
		{name: "invalid integer", args: []string{"-storage=postgres"}, env: map[string]string{"DATABASE_MAX_CONNS": "abc"}, wantErr: true},
		{name: "invalid log format", env: map[string]string{"LOG_FORMAT": "xml"}, wantErr: true},
		{name: "invalid log level", env: map[string]string{"LOG_LEVEL": "abc"}, wantErr: true},
		{name: "invalid address", args: []string{"-http-addr=localhost"}, wantErr: true},
		{name: "invalid port", args: []string{"-http-addr=:65536"}, wantErr: true},
		{name: "zero port", args: []string{"-http-addr=:0"}, wantErr: true},
		{name: "empty storage", env: map[string]string{"STORAGE": ""}, wantErr: true},
		{name: "empty timeout", env: map[string]string{"HTTP_READ_TIMEOUT": ""}, wantErr: true},
		{name: "zero operation timeout", args: []string{"-storage=postgres"}, env: map[string]string{"DATABASE_OPERATION_TIMEOUT": "0s"}, wantErr: true},
		{name: "invalid operation timeout", args: []string{"-storage=postgres"}, env: map[string]string{"DATABASE_OPERATION_TIMEOUT": "bad"}, wantErr: true},
		{name: "negative database timeout", args: []string{"-storage=postgres"}, env: map[string]string{"DATABASE_CONNECT_TIMEOUT": "-1s"}, wantErr: true},
		{name: "zero connections", args: []string{"-storage=postgres"}, env: map[string]string{"DATABASE_MAX_CONNS": "0"}, wantErr: true},
		{name: "integer overflow", env: map[string]string{"HTTP_MAX_BODY_BYTES": "9223372036854775808"}, wantErr: true},
		{name: "maximum port", args: []string{"-http-addr=:65535"}, wantStorage: "memory"},
		{name: "maximum connections", env: map[string]string{"DATABASE_MAX_CONNS": "2147483647"}, wantStorage: "memory"},
		{name: "unknown flag", args: []string{"-unknown"}, wantErr: true},
		{name: "positional argument", args: []string{"memory"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := load(tt.args, func(key string) (string, bool) { v, ok := tt.env[key]; return v, ok })
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg.Storage != tt.wantStorage {
				t.Fatalf("storage = %q", cfg.Storage)
			}
		})
	}
}

func TestLoadEnvironmentAndFlagOverrides(t *testing.T) {
	env := map[string]string{
		"STORAGE":                  "postgres",
		"HTTP_ADDR":                "invalid-env-address",
		"HTTP_READ_HEADER_TIMEOUT": "2s",
		"HTTP_READ_TIMEOUT":        "3s",
		"HTTP_WRITE_TIMEOUT":       "4s",
		"HTTP_IDLE_TIMEOUT":        "30s",
		"HTTP_SHUTDOWN_TIMEOUT":    "6s",
		"HTTP_MAX_BODY_BYTES":      "2048",
		"DATABASE_URL":             "postgres://localhost/test",
		"DATABASE_MAX_CONNS":       "25",
		"DATABASE_CONNECT_TIMEOUT": "7s",
		"LOG_LEVEL":                "debug",
		"LOG_FORMAT":               "text",
	}
	cfg, err := load([]string{"-storage=memory", "-http-addr=127.0.0.1:9090"}, func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		Storage: "memory",
		HTTP: HTTP{
			Addr:              "127.0.0.1:9090",
			ReadHeaderTimeout: 2 * time.Second,
			ReadTimeout:       3 * time.Second,
			WriteTimeout:      4 * time.Second,
			IdleTimeout:       30 * time.Second,
			ShutdownTimeout:   6 * time.Second,
			MaxBodyBytes:      2048,
		},
		Database: Database{},
		Memory:   Memory{MaxEntries: 100000},
		Log:      Log{Level: slog.LevelDebug, Format: "text"},
	}
	if cfg != want {
		t.Fatalf("configuration = %+v, want %+v", cfg, want)
	}
}

func TestLoadHelp(t *testing.T) {
	_, err := load([]string{"-h"}, func(string) (string, bool) { return "", false })
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}

func FuzzLoad(f *testing.F) {
	f.Add("memory", ":8080", "10s", "10")
	f.Add("postgres", "invalid", "-1s", "2147483648")
	f.Fuzz(func(t *testing.T, storage, addr, timeout, conns string) {
		env := map[string]string{"STORAGE": storage, "HTTP_ADDR": addr, "HTTP_READ_TIMEOUT": timeout, "DATABASE_MAX_CONNS": conns, "MEMORY_MAX_ENTRIES": conns}
		cfg, err := load(nil, func(key string) (string, bool) { v, ok := env[key]; return v, ok })
		if err == nil && (cfg.HTTP.ReadTimeout <= 0 || cfg.Database != (Database{}) || cfg.Storage != "memory") {
			t.Fatal("invalid configuration accepted")
		}
	})
}

func TestMemoryDoesNotReadDatabaseConfiguration(t *testing.T) {
	for _, args := range [][]string{nil, {"-storage=memory"}} {
		cfg, err := load(args, func(key string) (string, bool) {
			if key == "STORAGE" && args != nil {
				return "postgres", true
			}
			if strings.HasPrefix(key, "DATABASE_") {
				t.Fatalf("memory mode read %s", key)
			}
			return "", false
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Database != (Database{}) {
			t.Fatal("database configured in memory mode")
		}
	}
}

func TestPostgresFlagLoadsDatabaseConfiguration(t *testing.T) {
	cfg, err := load([]string{"-storage=postgres"}, func(key string) (string, bool) {
		values := map[string]string{"STORAGE": "memory", "DATABASE_URL": "postgres://localhost/test", "DATABASE_MAX_CONNS": "25"}
		v, ok := values[key]
		return v, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxConns != 25 || cfg.Database.OperationTimeout <= 0 {
		t.Fatal("missing postgres configuration")
	}
}
