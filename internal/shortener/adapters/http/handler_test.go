package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ozon-test-task/internal/shortener/application"
	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

type serviceStub struct {
	shorten func(context.Context, string) (domain.Link, error)
	resolve func(context.Context, string) (domain.Link, error)
}

func (s serviceStub) Shorten(ctx context.Context, raw string) (domain.Link, error) {
	return s.shorten(ctx, raw)
}

func (s serviceStub) Resolve(ctx context.Context, raw string) (domain.Link, error) {
	return s.resolve(ctx, raw)
}

func TestShortenInvalidRequests(t *testing.T) {
	for _, tt := range []struct {
		name, body, contentType string
		limit                   int64
		status                  int
	}{
		{"missing content type", `{"url":"https://example.com"}`, "", 1024, 415},
		{"wrong content type", `{}`, "text/plain", 1024, 415},
		{"malformed content type", `{}`, "application/json;", 1024, 400},
		{"invalid UTF-8", "{\"url\":\"https://example.com/\xff\"}", "application/json", 1024, 400},
		{"unpaired high surrogate", `{"url":"https://example.com/\ud800"}`, "application/json", 1024, 400},
		{"unpaired low surrogate", `{"url":"https://example.com/\udc00"}`, "application/json", 1024, 400},
		{"high surrogate followed by ASCII", `{"url":"https://example.com/\ud800\u0061"}`, "application/json", 1024, 400},
		{"high surrogate followed by escaped backslash", `{"url":"https://example.com/\ud800\\udc00"}`, "application/json", 1024, 400},
		{"empty", "", "application/json", 1024, 400},
		{"array", `[]`, "application/json", 1024, 400},
		{"null", `null`, "application/json", 1024, 400},
		{"no url", `{}`, "application/json", 1024, 400},
		{"null url", `{"url":null}`, "application/json", 1024, 400},
		{"number url", `{"url":123}`, "application/json", 1024, 400},
		{"object url", `{"url":{}}`, "application/json", 1024, 400},
		{"unknown", `{"url":"https://example.com","extra":1}`, "application/json", 1024, 400},
		{"duplicate", `{"url":"https://example.com","url":"https://other.com"}`, "application/json", 1024, 400},
		{"escaped duplicate", `{"url":"https://example.com","u\u0072l":"https://other.com"}`, "application/json", 1024, 400},
		{"case", `{"URL":"https://example.com"}`, "application/json", 1024, 400},
		{"extra json", `{"url":"https://example.com"} {}`, "application/json", 1024, 400},
		{"truncated", `{"url":"https://example.com"`, "application/json", 1024, 400},
		{"garbage", `{"url":"https://example.com"} x`, "application/json", 1024, 400},
		{"oversize", `{"url":"https://example.com"}`, "application/json", 10, 413},
		{"oversize trailing whitespace", `{"url":"https://example.com"}                         `, "application/json", 30, 413},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(serviceStub{shorten: func(context.Context, string) (domain.Link, error) {
				t.Fatal("service called for invalid body")
				return domain.Link{}, nil
			}}, "https://short.example")
			req := httptest.NewRequest(http.MethodPost, "/links", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rw := httptest.NewRecorder()
			req.Body = http.MaxBytesReader(rw, req.Body, tt.limit)
			h.Shorten(rw, req)
			if rw.Code != tt.status {
				t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
			}
			if rw.Header().Get("Content-Type") != "application/json" {
				t.Fatal("missing JSON content type")
			}
		})
	}
}

func TestServiceErrors(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrInvalidOriginalURL, 400, "invalid_request"},
		{domain.ErrInvalidCode, 400, "invalid_request"},
		{ports.ErrNotFound, 404, "not_found"},
		{ports.ErrCapacity, 503, "service_unavailable"},
		{application.ErrGenerationExhausted, 503, "service_unavailable"},
		{context.DeadlineExceeded, 504, "timeout"},
		{context.Canceled, 500, "internal_error"},
		{errors.New("database credentials"), 500, "internal_error"},
	} {
		for _, method := range []string{"POST", "GET"} {
			t.Run(method+tt.err.Error(), func(t *testing.T) {
				failure := func(context.Context, string) (domain.Link, error) {
					return domain.Link{}, fmt.Errorf("secret internal URL: %w", tt.err)
				}
				h := NewHandler(serviceStub{shorten: failure, resolve: failure}, "https://short.example")
				req := httptest.NewRequest(method, "/links/code", strings.NewReader(`{"url":"https://example.com"}`))
				req.Header.Set("Content-Type", "application/json")
				rw := httptest.NewRecorder()
				if method == "POST" {
					h.Shorten(rw, req)
				} else {
					h.Resolve(rw, req)
				}
				if rw.Code != tt.status || !strings.Contains(rw.Body.String(), `"code":"`+tt.code+`"`) {
					t.Fatalf("status = %d, body = %s", rw.Code, rw.Body)
				}
				if strings.Contains(rw.Body.String(), "secret") || strings.Contains(rw.Body.String(), "credentials") {
					t.Fatal("internal details exposed")
				}
			})
		}
	}
}

func TestSuccessAndContext(t *testing.T) {
	original := "https://example.com/a%2Fb?q=%2f&x=1#Fragment"
	url, err := domain.NewOriginalURL(original)
	if err != nil {
		t.Fatal(err)
	}
	code, err := domain.NewCode("Abc123_Xyz")
	if err != nil {
		t.Fatal(err)
	}
	link, err := domain.NewLink(url, code)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	h := NewHandler(serviceStub{
		shorten: func(got context.Context, raw string) (domain.Link, error) {
			if got != ctx || got.Err() != context.Canceled || raw != original {
				t.Fatal("request context or URL lost")
			}
			return link, nil
		},
		resolve: func(got context.Context, raw string) (domain.Link, error) {
			if got != ctx || raw != code.String() {
				t.Fatal("request context or code lost")
			}
			return link, nil
		},
	}, "https://short.example/prefix")
	req := httptest.NewRequest("POST", "http://untrusted.example/links", strings.NewReader(`{"url":"`+original+`"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("X-Forwarded-Host", "untrusted.example")
	rw := httptest.NewRecorder()
	h.Shorten(rw, req)
	if rw.Code != 200 || rw.Body.String() != "{\"short_url\":\"https://short.example/prefix/links/Abc123_Xyz\"}\n" {
		t.Fatalf("POST = %d %s", rw.Code, rw.Body)
	}
	req = httptest.NewRequest("GET", "/links/Abc123_Xyz", nil).WithContext(ctx)
	req.SetPathValue("code", code.String())
	rw = httptest.NewRecorder()
	h.Resolve(rw, req)
	if rw.Code != 200 || rw.Header().Get("Location") != "" || !strings.Contains(rw.Body.String(), "a%2Fb?q=%2f") {
		t.Fatalf("GET = %d %s", rw.Code, rw.Body)
	}
}

func TestShortenPreservesUnicode(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"UTF-8", `{"url":"https://example.com/путь"}`, "https://example.com/путь"},
		{"surrogate pair", `{"url":"https://example.com/\ud83d\ude00"}`, "https://example.com/😀"},
		{"escaped backslash", `{"url":"https://example.com/?q=\\ud800"}`, `https://example.com/?q=\ud800`},
		{"replacement character", `{"url":"https://example.com/\ufffd"}`, "https://example.com/�"},
		{"escaped BMP character", `{"url":"https://example.com/\u043f"}`, "https://example.com/п"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original, err := domain.NewOriginalURL(tt.want)
			if err != nil {
				t.Fatal(err)
			}
			code, err := domain.NewCode("Abc123_Xyz")
			if err != nil {
				t.Fatal(err)
			}
			link, err := domain.NewLink(original, code)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			h := NewHandler(serviceStub{shorten: func(_ context.Context, raw string) (domain.Link, error) {
				called = true
				if raw != tt.want {
					t.Fatalf("URL changed during JSON decoding: got %q, want %q", raw, tt.want)
				}
				return link, nil
			}}, "https://short.example")
			req := httptest.NewRequest(http.MethodPost, "/links", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rw := httptest.NewRecorder()
			h.Shorten(rw, req)
			if rw.Code != http.StatusOK || !called {
				t.Fatalf("POST = %d %s; service called = %v", rw.Code, rw.Body, called)
			}
		})
	}
}

// Malformed request bodies must produce errors rather than panic in Unicode validation.
func FuzzDecodeRequest(f *testing.F) {
	for _, body := range []string{
		`{"url":"https://example.com"}`,
		`{"url":"https://example.com/\ud83d\ude00"}`,
		`{"url":"https://example.com/\ud800"}`,
		`{"url":"https://example.com/\udc00"}`,
		`{"url":"https://example.com/\ud800\\udc00"}`,
		`{"url":"https://example.com/\\"}`,
		`{"url":null}`, `{"url":[]}`, `{"url":{}}`, `{"url":"\u"}`,
		"{\"url\":\"https://example.com/\xff\"}",
	} {
		f.Add(body)
	}
	f.Fuzz(func(_ *testing.T, body string) {
		_, _ = decodeRequest(strings.NewReader(body))
	})
}
