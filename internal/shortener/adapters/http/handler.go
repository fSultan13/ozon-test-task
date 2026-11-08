package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"ozon-test-task/internal/shortener/application"
	"ozon-test-task/internal/shortener/domain"
	"ozon-test-task/internal/shortener/ports"
)

type Service interface {
	Shorten(context.Context, string) (domain.Link, error)
	Resolve(context.Context, string) (domain.Link, error)
}

type Handler struct {
	service       Service
	publicBaseURL string
}

// NewHandler requires a normalized public base URL validated by configuration.
func NewHandler(service Service, publicBaseURL string) *Handler {
	return &Handler{service: service, publicBaseURL: publicBaseURL}
}

type ShortenRequest struct {
	URL string `json:"url" example:"https://example.com" validate:"required"`
}

type ShortenResponse struct {
	ShortURL string `json:"short_url" example:"https://short.example/links/Abc123_Xyz"`
}

type ResolveResponse struct {
	URL string `json:"url" example:"https://example.com" validate:"required"`
}

type ErrorResponse struct {
	Code    string `json:"code" example:"invalid_request"`
	Message string `json:"message" example:"Invalid request"`
}

// Shorten saves an original URL or returns its existing short URL.
// @Summary Shorten a URL
// @Description Returns the same short URL with status 200 for repeated submissions. The public address is configured by PUBLIC_BASE_URL.
// @Tags links
// @Accept json
// @Produce json
// @Param request body ShortenRequest true "Absolute HTTP(S) URL, at most 8000 bytes; only the url field is accepted"
// @Success 200 {object} ShortenResponse
// @Failure 400 {object} ErrorResponse
// @Failure 413 {object} ErrorResponse
// @Failure 415 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 504 {object} ErrorResponse
// @Router /links [post]
func (h *Handler) Shorten(w http.ResponseWriter, r *http.Request) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Unsupported media type")
		return
	}
	input, err := decodeRequest(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "Payload too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request")
		}
		return
	}
	link, err := h.service.Shorten(r.Context(), input.URL)
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ShortenResponse{ShortURL: h.publicBaseURL + "/links/" + link.Code().String()})
}

// Resolve returns the original URL without redirecting.
// @Summary Resolve a short URL code
// @Description Returns the exact stored original URL as JSON, without redirecting.
// @Tags links
// @Produce json
// @Param code path string true "Short code: 1 to 10 ASCII letters, digits or underscores" minlength(1) maxlength(10)
// @Success 200 {object} ResolveResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Failure 503 {object} ErrorResponse
// @Failure 504 {object} ErrorResponse
// @Router /links/{code} [get]
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	link, err := h.service.Resolve(r.Context(), r.PathValue("code"))
	if err != nil {
		serviceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ResolveResponse{URL: link.OriginalURL().String()})
}

// Decode tokens explicitly: encoding/json otherwise accepts duplicate keys and
// matches struct field names without regard to case.
func decodeRequest(body io.Reader) (ShortenRequest, error) {
	invalid := errors.New("invalid JSON request")
	decoder := json.NewDecoder(body)
	token, err := decoder.Token()
	if err != nil {
		return ShortenRequest{}, err
	}
	if token != json.Delim('{') {
		return ShortenRequest{}, invalid
	}
	var input ShortenRequest
	seen := false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ShortenRequest{}, err
		}
		if key != "url" || seen {
			return ShortenRequest{}, invalid
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return ShortenRequest{}, err
		}
		if !validUnicodeString(raw) {
			return ShortenRequest{}, invalid
		}
		if err := json.Unmarshal(raw, &input.URL); err != nil {
			return ShortenRequest{}, invalid
		}
		seen = true
	}

	token, err = decoder.Token()
	if err != nil {
		return ShortenRequest{}, err
	}
	if token != json.Delim('}') || !seen {
		return ShortenRequest{}, invalid
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return ShortenRequest{}, err
		}
		return ShortenRequest{}, invalid
	}

	return input, nil
}

// validUnicodeString checks a syntactically valid JSON value before Unmarshal
// can silently replace invalid UTF-8 or unpaired UTF-16 surrogates with U+FFFD.
func validUnicodeString(raw []byte) bool {
	if len(raw) < 2 || raw[0] != '"' || !utf8.Valid(raw) {
		return false
	}
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		value, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if !utf16.IsSurrogate(rune(value)) {
			continue
		}
		if value > 0xdbff || i+6 >= len(raw) || string(raw[i+1:i+3]) != `\u` {
			return false
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func serviceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidOriginalURL), errors.Is(err, domain.ErrInvalidCode):
		writeError(w, 400, "invalid_request", "Invalid request")
	case errors.Is(err, ports.ErrNotFound):
		writeError(w, 404, "not_found", "Link not found")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 504, "timeout", "Operation timed out")
	case errors.Is(err, ports.ErrCapacity), errors.Is(err, application.ErrGenerationExhausted):
		writeError(w, 503, "service_unavailable", "Service unavailable")
	default:
		writeError(w, 500, "internal_error", "Internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
