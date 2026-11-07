package config

import (
	"fmt"
	"strconv"
	"time"
)

// envLookup allows configuration tests to supply an isolated environment.
type envLookup func(string) (string, bool)

func (lookup envLookup) stringValue(key, fallback string) string {
	if value, ok := lookup(key); ok {
		return value
	}
	return fallback
}

func (lookup envLookup) positiveDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration", key)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}

	return duration, nil
}

func (lookup envLookup) integer(key string, fallback int64) (int64, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}

	integer, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", key)
	}

	return integer, nil
}
