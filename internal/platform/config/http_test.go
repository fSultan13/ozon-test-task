package config

import (
	"strings"
	"testing"
)

func loadTest(args []string, env envLookup) (Config, error) {
	return load(args, func(key string) (string, bool) {
		value, ok := env(key)
		if key == "PUBLIC_BASE_URL" && !ok {
			return "http://localhost:8080", true
		}
		return value, ok
	})
}

func TestPublicBaseURL(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{"https://short.example/", "https://short.example"},
		{"http://localhost:8080/prefix///", "http://localhost:8080/prefix"},
		{"https://short.example/a%20b/", "https://short.example/a%20b"},
		{"", ""},
		{"/relative", ""},
		{"ftp://example.com", ""},
		{"https://user:secret@example.com", ""},
		{"https://example.com?", ""},
		{"https://example.com#", ""},
		{"https://example.com/a b", ""},
		{"https://example.com:bad", ""},
		{"https://", ""},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			cfg, err := load(nil, func(key string) (string, bool) { return tt.raw, key == "PUBLIC_BASE_URL" })
			if tt.want == "" {
				if err == nil {
					t.Fatal("invalid base URL accepted")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatal("credentials leaked")
				}
			} else if err != nil || cfg.PublicBaseURL != tt.want {
				t.Fatalf("base = %q, error = %v", cfg.PublicBaseURL, err)
			}
		})
	}
	_, err := load(nil, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("missing PUBLIC_BASE_URL accepted")
	}
}

func TestShortenerOperationTimeout(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "bad", "", "2s"} {
		t.Run(value, func(t *testing.T) {
			cfg, err := loadTest(nil, func(key string) (string, bool) { return value, key == "SHORTENER_OPERATION_TIMEOUT" })
			if (err == nil) != (value == "2s") {
				t.Fatalf("timeout error = %v", err)
			}
			if err == nil && cfg.ShortenerOperationTimeout.String() != value {
				t.Fatal("timeout not applied")
			}
		})
	}
}
