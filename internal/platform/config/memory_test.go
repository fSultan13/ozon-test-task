package config

import "testing"

func TestMemoryConfiguration(t *testing.T) {
	for _, tt := range []struct {
		key, value string
		invalid    bool
	}{
		{"MEMORY_FILE", "", true},
		{"MEMORY_BATCH_SIZE", "0", true},
		{"MEMORY_BATCH_SIZE", "bad", true},
		{"MEMORY_MAX_PENDING_WRITES", "0", true},
		{"MEMORY_BATCH_DELAY", "0s", true},
		{"MEMORY_BATCH_DELAY", "3s", true},
		{"MEMORY_BATCH_SIZE", "1", false},
		{"MEMORY_CACHE_ENTRIES", "-1", true},
		{"MEMORY_CACHE_ENTRIES", "bad", true},
		{"MEMORY_MAX_DISK_BYTES", "0", true},
		{"MEMORY_MAX_DISK_BYTES", "bad", true},
		{"MEMORY_MAX_DISK_BYTES", "9223372036854775808", true},
		{"MEMORY_OPERATION_TIMEOUT", "0s", true},
		{"MEMORY_OPERATION_TIMEOUT", "bad", true},
		{"MEMORY_CACHE_ENTRIES", "0", false},
		{"MEMORY_MAX_DISK_BYTES", "65536", false},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			_, err := load([]string{"-storage=memory"}, func(key string) (string, bool) {
				if key == tt.key {
					return tt.value, true
				}
				return "", false
			})
			if (err != nil) != tt.invalid {
				t.Fatalf("error = %v", err)
			}
		})
	}
	cfg, err := load([]string{"-storage=memory"}, func(key string) (string, bool) {
		if key == "STORAGE" {
			return "postgres", true
		}
		if len(key) >= 9 && key[:9] == "DATABASE_" {
			t.Fatalf("memory read %s", key)
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database != (Database{}) || cfg.Memory.CacheEntries != 10000 || cfg.Memory.MaxDiskBytes != 1<<30 {
		t.Fatalf("unexpected memory defaults: %+v", cfg)
	}
}
