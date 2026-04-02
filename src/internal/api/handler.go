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

type VerifyRequest struct {
	Email string `json:"email"`
}

type BatchVerifyRequest struct {
	Emails []string `json:"emails"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	Status string `json:"status"`
}

type Handler struct {
	prober *smtp.Prober
}

func NewHandler(prober *smtp.Prober) *Handler {
	return &Handler{prober: prober}
}

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

type ValidationError struct {
	Msg string
}

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
