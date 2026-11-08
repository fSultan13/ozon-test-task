package config

import (
	"errors"
	"math"
)

func loadMemory(env envLookup) (Memory, error) {
	entries, err := env.integer("MEMORY_MAX_ENTRIES", 100000)
	if err != nil {
		return Memory{}, err
	}
	if entries < 1 || entries > math.MaxInt {
		return Memory{}, errors.New("MEMORY_MAX_ENTRIES must fit a positive int")
	}
	return Memory{MaxEntries: int(entries)}, nil
}
