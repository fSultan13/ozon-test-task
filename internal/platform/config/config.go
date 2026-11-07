package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Storage  string
	HTTP     HTTP
	Database Database
	Log      Log
}

type HTTP struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxBodyBytes      int64
}

type Database struct {
	URL              string
	MaxConns         int32
	ConnectTimeout   time.Duration
	OperationTimeout time.Duration
}

type Log struct {
	Level  slog.Level
	Format string
}

// Load applies flags over environment variables over defaults. It does not load .env files.
func Load(args []string) (Config, error) {
	return load(args, os.LookupEnv)
}

func load(args []string, env envLookup) (Config, error) {
	httpConfig, err := loadHTTP(env)
	if err != nil {
		return Config{}, err
	}

	databaseConfig, err := loadDatabase(env)
	if err != nil {
		return Config{}, err
	}

	logConfig, err := loadLog(env)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Storage:  env.stringValue("STORAGE", storageMemory),
		HTTP:     httpConfig,
		Database: databaseConfig,
		Log:      logConfig,
	}

	if err := applyFlags(args, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func loadHTTP(env envLookup) (HTTP, error) {
	cfg := HTTP{Addr: env.stringValue("HTTP_ADDR", defaultHTTPAddr)}

	timeouts := []struct {
		key      string
		fallback time.Duration
		target   *time.Duration
	}{
		{"HTTP_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout, &cfg.ReadHeaderTimeout},
		{"HTTP_READ_TIMEOUT", defaultReadTimeout, &cfg.ReadTimeout},
		{"HTTP_WRITE_TIMEOUT", defaultWriteTimeout, &cfg.WriteTimeout},
		{"HTTP_IDLE_TIMEOUT", defaultIdleTimeout, &cfg.IdleTimeout},
		{"HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout, &cfg.ShutdownTimeout},
	}
	for _, timeout := range timeouts {
		value, err := env.positiveDuration(timeout.key, timeout.fallback)
		if err != nil {
			return HTTP{}, err
		}
		*timeout.target = value
	}

	maxBodyBytes, err := env.integer("HTTP_MAX_BODY_BYTES", defaultMaxBodyBytes)
	if err != nil {
		return HTTP{}, err
	}
	if maxBodyBytes <= 0 {
		return HTTP{}, errors.New("HTTP_MAX_BODY_BYTES must be positive")
	}
	cfg.MaxBodyBytes = maxBodyBytes

	return cfg, nil
}

func loadDatabase(env envLookup) (Database, error) {
	maxConns, err := env.integer("DATABASE_MAX_CONNS", defaultDatabaseMaxConns)
	if err != nil {
		return Database{}, err
	}
	if maxConns < 1 || maxConns > math.MaxInt32 {
		return Database{}, fmt.Errorf("DATABASE_MAX_CONNS must be between 1 and %d", math.MaxInt32)
	}

	connectTimeout, err := env.positiveDuration("DATABASE_CONNECT_TIMEOUT", defaultDatabaseConnectTimeout)
	if err != nil {
		return Database{}, err
	}

	operationTimeout, err := env.positiveDuration("DATABASE_OPERATION_TIMEOUT", defaultDatabaseOperationTimeout)
	if err != nil {
		return Database{}, err
	}

	return Database{
		URL:              env.stringValue("DATABASE_URL", ""),
		MaxConns:         int32(maxConns),
		ConnectTimeout:   connectTimeout,
		OperationTimeout: operationTimeout,
	}, nil
}

func loadLog(env envLookup) (Log, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(env.stringValue("LOG_LEVEL", defaultLogLevel))); err != nil {
		return Log{}, errors.New("invalid LOG_LEVEL")
	}

	format := env.stringValue("LOG_FORMAT", logFormatJSON)
	if format != logFormatJSON && format != logFormatText {
		return Log{}, errors.New("LOG_FORMAT must be json or text")
	}

	return Log{Level: level, Format: format}, nil
}

func applyFlags(args []string, cfg *Config) error {
	flags := flag.NewFlagSet("api", flag.ContinueOnError)
	// The caller handles errors and -h; avoid duplicate output from the parser.
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.Storage, "storage", cfg.Storage, "storage: memory or postgres")
	flags.StringVar(&cfg.HTTP.Addr, "http-addr", cfg.HTTP.Addr, "HTTP listen address")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}

	return nil
}

func (cfg Config) validate() error {
	if cfg.Storage != storageMemory && cfg.Storage != storagePostgres {
		return errors.New("storage must be memory or postgres")
	}
	if cfg.Storage == storagePostgres && cfg.Database.URL == "" {
		return errors.New("DATABASE_URL is required for postgres")
	}

	return validateHTTPAddr(cfg.HTTP.Addr)
}

func validateHTTPAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return errors.New("http address must be host:port")
	}

	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < minHTTPPort || portNumber > maxHTTPPort {
		return fmt.Errorf("HTTP port must be between %d and %d", minHTTPPort, maxHTTPPort)
	}

	return nil
}
