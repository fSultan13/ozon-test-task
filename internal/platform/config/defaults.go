package config

import "time"

const (
	storageMemory   = "memory"
	storagePostgres = "postgres"

	defaultHTTPAddr          = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultWriteTimeout      = 15 * time.Second
	defaultIdleTimeout       = time.Minute
	defaultShutdownTimeout   = 10 * time.Second
	defaultMaxBodyBytes      = 1 << 20 // 1 MiB.

	minHTTPPort = 1
	maxHTTPPort = 65535

	defaultDatabaseMaxConns         = 10
	defaultDatabaseConnectTimeout   = 5 * time.Second
	defaultDatabaseOperationTimeout = 3 * time.Second

	defaultLogLevel = "info"
	logFormatJSON   = "json"
	logFormatText   = "text"
)
