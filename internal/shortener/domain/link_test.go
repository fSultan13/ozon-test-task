package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestOriginalURLLength(t *testing.T) {
	const prefix = "https://example.com/"
	remaining := MaxOriginalURLBytes - len(prefix)
	for _, tt := range []struct {
		name, raw string
		valid     bool
	}{
		{"empty", "", false},
		{"ordinary", "https://example.com/a?x=1#part", true},
		{"limit", prefix + strings.Repeat("a", remaining), true},
		{"over limit", prefix + strings.Repeat("a", remaining+1), false},
		{"multibyte at limit", prefix + strings.Repeat("я", remaining/2) + strings.Repeat("a", remaining%2), true},
		{"multibyte over limit", prefix + strings.Repeat("я", remaining/2+1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original, err := NewOriginalURL(tt.raw)
			if tt.valid {
				if err != nil || original.String() != tt.raw {
					t.Fatalf("input was rejected or changed: %v", err)
				}
			} else if !errors.Is(err, ErrInvalidOriginalURL) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestOriginalURLSyntax(t *testing.T) {
	for _, raw := range []string{
		"http://example.com",
		"HTTPS://Example.com/a?x=1&x=2#part",
		"https://example.com/a%20b?q=a+b&next=%2Fpath#some%20part",
		"http://localhost:8080/path",
		"http://127.0.0.1/path",
		"http://[::1]:8080/path",
		"https://пример.рф/путь?ключ=значение#часть",
		"https://user:password@example.com/path",
	} {
		t.Run(raw, func(t *testing.T) {
			original, err := NewOriginalURL(raw)
			if err != nil || original.String() != raw {
				t.Fatalf("valid URL rejected or changed: %v", err)
			}
		})
	}
	for _, raw := range []string{
		"example.com", "/path", "//example.com/path",
		"ftp://example.com", "javascript:alert(1)", "mailto:user@example.com",
		"https:", "https:example.com", "https:///path", "https://:8080/path",
		"https://example.com:bad/path", "https://[::1/path",
		" https://example.com", "https://example.com ", "https://example.com/a b",
		"https://example.com/?q=a b", "https://example.com/#a b",
		"https://example.com/\t", "https://example.com/\n", "https://example.com/\x00",
		"https://example.com/\u007f", "https://example.com/\u00a0", "https://example.com/\xff",
		"https://example.com/%zz", "https://example.com/?q=%zz", "https://example.com/#%zz",
		"https://user:secret@example.com/%zz",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := NewOriginalURL(raw)
			if !errors.Is(err, ErrInvalidOriginalURL) {
				t.Fatalf("invalid URL accepted: %v", err)
			}
			if strings.Contains(err.Error(), raw) {
				t.Fatal("validation error discloses the original URL")
			}
		})
	}
}

func BenchmarkNewOriginalURL(b *testing.B) {
	const prefix = "https://example.com/"
	for _, tt := range []struct{ name, raw string }{
		{"ordinary", prefix + "a?x=1#part"},
		{"limit", prefix + strings.Repeat("a", MaxOriginalURLBytes-len(prefix))},
	} {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := NewOriginalURL(tt.raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestCode(t *testing.T) {
	for _, raw := range []string{"a", "aZ09_", "abcdefghij"} {
		code, err := NewCode(raw)
		if err != nil || code.String() != raw {
			t.Fatalf("valid code rejected: %v", err)
		}
	}
	for _, c := range CodeAlphabet {
		if _, err := NewCode(string(c)); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{"", "abcdefghijk", "abc-def", "abc/def", "a b", "я", "a\n", "a\x00"} {
		if _, err := NewCode(raw); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("invalid code accepted: %v", err)
		}
	}
}

func TestLinkRejectsZeroValues(t *testing.T) {
	original, err := NewOriginalURL("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	code, err := NewCode("aZ_0123456")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLink(OriginalURL{}, code); !errors.Is(err, ErrInvalidOriginalURL) {
		t.Fatalf("error = %v", err)
	}
	if _, err := NewLink(original, Code{}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("error = %v", err)
	}
	link, err := NewLink(original, code)
	if err != nil || link.OriginalURL() != original || link.Code() != code {
		t.Fatalf("association was not preserved: %v", err)
	}
}
