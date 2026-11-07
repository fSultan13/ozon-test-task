package domain

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxOriginalURLBytes is a service limit based on RFC 9110's recommended
	// minimum URI support, not a maximum imposed by the URI standard.
	MaxOriginalURLBytes = 8000
	MaxCodeLength       = 10
	CodeAlphabet        = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
)

var (
	ErrInvalidOriginalURL = errors.New("original URL must be an absolute HTTP(S) URL with a host, no whitespace or control characters, and at most 8000 bytes")
	ErrInvalidCode        = errors.New("code must contain 1 to 10 ASCII letters, digits or underscores")
)

// OriginalURL validates an absolute HTTP(S) URL and preserves the exact input.
// Validation does not resolve the host or check the destination's availability.
type OriginalURL struct{ value string }

func NewOriginalURL(value string) (OriginalURL, error) {
	if len(value) == 0 || len(value) > MaxOriginalURLBytes {
		return OriginalURL{}, ErrInvalidOriginalURL
	}

	if !utf8.ValidString(value) || strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return OriginalURL{}, ErrInvalidOriginalURL
	}

	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		// url.Parse errors can contain the original URL, including credentials.
		// Return only the domain error instead of retaining that input.
		return OriginalURL{}, ErrInvalidOriginalURL
	}

	// Parse validates escaping in the path and fragment, but leaves RawQuery
	// untouched. Check its escaping without interpreting query parameters.
	if _, err := url.QueryUnescape(parsed.RawQuery); err != nil {
		return OriginalURL{}, ErrInvalidOriginalURL
	}

	return OriginalURL{value: value}, nil
}

func (u OriginalURL) String() string { return u.value }

// Code is the short identifier, without a host or path prefix.
type Code struct{ value string }

func NewCode(value string) (Code, error) {
	if len(value) == 0 || len(value) > MaxCodeLength {
		return Code{}, ErrInvalidCode
	}
	for i := range len(value) {
		if strings.IndexByte(CodeAlphabet, value[i]) < 0 {
			return Code{}, ErrInvalidCode
		}
	}
	return Code{value: value}, nil
}

func (c Code) String() string { return c.value }

// Link is an immutable association. Its zero value must not be persisted.
type Link struct {
	original OriginalURL
	code     Code
}

func NewLink(original OriginalURL, code Code) (Link, error) {
	if original.value == "" {
		return Link{}, ErrInvalidOriginalURL
	}
	if code.value == "" {
		return Link{}, ErrInvalidCode
	}
	return Link{original: original, code: code}, nil
}

func (l Link) OriginalURL() OriginalURL { return l.original }
func (l Link) Code() Code               { return l.code }
