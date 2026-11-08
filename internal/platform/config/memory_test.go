package config

import "testing"

func TestMemoryConfiguration(t *testing.T) {
	for _, tt := range []struct {
		key, value string
		invalid    bool
	}{
		{"MEMORY_MAX_ENTRIES", "0", true},
		{"MEMORY_MAX_ENTRIES", "-1", true},
		{"MEMORY_MAX_ENTRIES", "bad", true},
		{"MEMORY_MAX_ENTRIES", "9223372036854775808", true},
		{"MEMORY_MAX_ENTRIES", "1", false},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			_, err := loadTest([]string{"-storage=memory"}, func(key string) (string, bool) {
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
	cfg, err := loadTest([]string{"-storage=memory"}, func(key string) (string, bool) {
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
	if cfg.Database != (Database{}) || cfg.Memory.MaxEntries != 100000 {
		t.Fatalf("unexpected memory defaults: %+v", cfg)
	}
}
