package config

import (
	"errors"
	"math"
	"time"
)

func loadMemory(env envLookup) (Memory, error) {
	cfg := Memory{File: env.stringValue("MEMORY_FILE", "data/links.db")}
	if cfg.File == "" {
		return Memory{}, errors.New("MEMORY_FILE must not be empty")
	}

	entries, err := env.integer("MEMORY_CACHE_ENTRIES", 10000)
	if err != nil {
		return Memory{}, err
	}
	if entries < 0 || entries > math.MaxInt {
		return Memory{}, errors.New("MEMORY_CACHE_ENTRIES must fit a nonnegative int")
	}

	diskBytes, err := env.integer("MEMORY_MAX_DISK_BYTES", 1<<30)
	if err != nil {
		return Memory{}, err
	}
	if diskBytes < 65536 || diskBytes > math.MaxInt {
		return Memory{}, errors.New("MEMORY_MAX_DISK_BYTES must fit an int and be at least 65536")
	}

	cfg.OperationTimeout, err = env.positiveDuration("MEMORY_OPERATION_TIMEOUT", 3*time.Second)
	if err != nil {
		return Memory{}, err
	}

	cfg.CacheEntries, cfg.MaxDiskBytes = int(entries), int(diskBytes)

	cfg.BatchDelay, err = env.positiveDuration("MEMORY_BATCH_DELAY", time.Millisecond)
	if err != nil {
		return Memory{}, err
	}
	if cfg.BatchDelay >= cfg.OperationTimeout {
		return Memory{}, errors.New("MEMORY_BATCH_DELAY must be shorter than MEMORY_OPERATION_TIMEOUT")
	}

	for _, limit := range []struct {
		key      string
		fallback int64
		target   *int
	}{
		{"MEMORY_BATCH_SIZE", 128, &cfg.BatchSize},
		{"MEMORY_MAX_PENDING_WRITES", 1024, &cfg.MaxPendingWrites},
	} {
		value, err := env.integer(limit.key, limit.fallback)
		if err != nil {
			return Memory{}, err
		}
		if value < 1 || value > math.MaxInt {
			return Memory{}, errors.New(limit.key + " must fit a positive int")
		}
		*limit.target = int(value)
	}
	return cfg, nil
}
