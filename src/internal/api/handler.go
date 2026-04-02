package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sonquer/mailprobe/internal/smtp"
	"github.com/sonquer/mailprobe/internal/version"
)

const maxBatchSize = 50

// VerifyRequest is the JSON request body for the POST /verify endpoint.
type VerifyRequest struct {
	Email string `json:"email"`
}

// BatchVerifyRequest is the JSON request body for the POST /verify/batch endpoint.
type BatchVerifyRequest struct {
	Emails []string `json:"emails"`
}

// ErrorResponse is the JSON response body returned for all error responses.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse is the JSON response body for the GET /health endpoint.
type HealthResponse struct {
	Status string `json:"status"`
}

// Handler holds HTTP handlers for the mailprobe API.
type Handler struct {
	prober *smtp.Prober
}

// NewHandler creates a Handler backed by the given SMTP prober.
func NewHandler(prober *smtp.Prober) *Handler {
	return &Handler{prober: prober}
}

// RegisterRoutes binds all API endpoint handlers to the given ServeMux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/verify", h.handleVerify)
	mux.HandleFunc("/verify/batch", h.handleBatchVerify)
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/version", h.handleVersion)
}

func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}

	var req VerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := ValidateEmail(req.Email); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result := h.prober.Probe(r.Context(), req.Email)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleBatchVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	if !isJSON(r) {
		writeError(w, http.StatusUnsupportedMediaType, "content-type must be application/json")
		return
	}

	var req BatchVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if len(req.Emails) == 0 {
		writeError(w, http.StatusBadRequest, "emails array must not be empty")
		return
	}

	if len(req.Emails) > maxBatchSize {
		writeError(w, http.StatusBadRequest, "too many emails, maximum is 50")
		return
	}

	for _, email := range req.Emails {
		if err := ValidateEmail(email); err != nil {
			writeError(w, http.StatusBadRequest, "invalid email "+email+": "+err.Error())
			return
		}
	}

	result := h.prober.ProbeBatch(r.Context(), req.Emails)
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	writeJSON(w, http.StatusOK, version.Get())
}

// ValidateEmail checks that the given string is a valid email address format
// with a local part, @ sign, and a domain containing at least one dot.
func ValidateEmail(email string) error {
	if email == "" {
		return &ValidationError{"email must not be empty"}
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return &ValidationError{"invalid email format"}
	}
	if !strings.Contains(parts[1], ".") {
		return &ValidationError{"invalid email domain"}
	}
	return nil
}

// ValidationError represents an email validation failure.
type ValidationError struct {
	Msg string
}

// Error returns the validation error message.
func (e *ValidationError) Error() string {
	return e.Msg
}

func isJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/json")
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, ErrorResponse{Error: msg})
}

// LoggingMiddleware wraps an http.Handler to log each request's method, path,
// status code, and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// RecoveryMiddleware wraps an http.Handler to recover from panics and return
// a 500 Internal Server Error response instead of crashing the server.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered", "error", err)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// AuthMiddleware wraps an http.Handler to require a valid API key in the
// X-API-Key header. If keys is empty, authentication is disabled and the
// handler is returned unwrapped. The /health and /version endpoints always
// bypass authentication.
func AuthMiddleware(keys []string, next http.Handler) http.Handler {
	if len(keys) == 0 {
		return next
	}

	allowed := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		allowed[k] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/version" {
			next.ServeHTTP(w, r)
			return
		}

		key := r.Header.Get("X-API-Key")
		if _, ok := allowed[key]; !ok {
			writeError(w, http.StatusUnauthorized, "invalid or missing API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
