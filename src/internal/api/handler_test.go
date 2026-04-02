package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sonquer/mailprobe/internal/config"
	"github.com/sonquer/mailprobe/internal/smtp"
	"github.com/sonquer/mailprobe/internal/version"
)

func newTestHandler() *Handler {
	cfg := config.Config{
		Port:        "8080",
		SMTPTimeout: 5 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
	}
	prober := smtp.NewProber(cfg)
	return NewHandler(prober)
}

func TestHealthEndpoint(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp HealthResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "ok" {
		t.Errorf("expected status ok, got %s", resp.Status)
	}
}

func TestHealthEndpointWrongMethod(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestVerifyWrongMethod(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestVerifyWrongContentType(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBufferString(`{"email":"a@b.com"}`))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 415, got %d", w.Code)
	}
}

func TestVerifyEmptyBody(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBufferString(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestVerifyEmptyEmail(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(VerifyRequest{Email: ""})
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestVerifyInvalidEmailNoAt(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(VerifyRequest{Email: "notanemail"})
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestVerifyInvalidEmailNoDomain(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(VerifyRequest{Email: "user@"})
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestVerifyInvalidEmailNoDot(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(VerifyRequest{Email: "user@domain"})
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestVerifyInvalidJSON(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewBufferString("{broken"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestBatchVerifyWrongMethod(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/verify/batch", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestBatchVerifyEmptyEmails(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(BatchVerifyRequest{Emails: []string{}})
	req := httptest.NewRequest(http.MethodPost, "/verify/batch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestBatchVerifyTooManyEmails(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	emails := make([]string, 51)
	for i := range emails {
		emails[i] = "user@example.com"
	}
	body, _ := json.Marshal(BatchVerifyRequest{Emails: emails})
	req := httptest.NewRequest(http.MethodPost, "/verify/batch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestBatchVerifyInvalidEmail(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(BatchVerifyRequest{Emails: []string{"valid@example.com", "invalid"}})
	req := httptest.NewRequest(http.MethodPost, "/verify/batch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestValidateEmailCases(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"user@example.com", true},
		{"a@b.co", true},
		{"user+tag@domain.org", true},
		{"", false},
		{"noatsign", false},
		{"@domain.com", false},
		{"user@", false},
		{"user@domain", false},
		{"@", false},
	}

	for _, tt := range tests {
		t.Run(tt.email, func(t *testing.T) {
			err := ValidateEmail(tt.email)
			if tt.valid && err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
			if !tt.valid && err == nil {
				t.Errorf("expected invalid, got nil error")
			}
		})
	}
}

func TestLoggingMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := LoggingMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	handler := RecoveryMiddleware(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestIntegrationVerifyDeliverable(t *testing.T) {
	accepted := map[string]int{"user@example.com": 250}
	httpServer, smtpServer := setupIntegrationServer(t, false, accepted)
	defer httpServer.Close()
	defer smtpServer.Close()

	body, _ := json.Marshal(VerifyRequest{Email: "user@example.com"})
	resp, err := http.Post(httpServer.URL+"/verify", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var result smtp.VerifyResult
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Result != smtp.ResultDeliverable {
		t.Errorf("expected deliverable, got %s", result.Result)
	}
	if result.SMTPCode != 250 {
		t.Errorf("expected SMTP code 250, got %d", result.SMTPCode)
	}
}

func TestIntegrationVerifyUndeliverable(t *testing.T) {
	httpServer, smtpServer := setupIntegrationServer(t, false, nil)
	defer httpServer.Close()
	defer smtpServer.Close()

	body, _ := json.Marshal(VerifyRequest{Email: "nobody@example.com"})
	resp, err := http.Post(httpServer.URL+"/verify", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result smtp.VerifyResult
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Result != smtp.ResultUndeliverable {
		t.Errorf("expected undeliverable, got %s", result.Result)
	}
}

func TestIntegrationBatchVerify(t *testing.T) {
	accepted := map[string]int{"exists@example.com": 250}
	httpServer, smtpServer := setupIntegrationServer(t, false, accepted)
	defer httpServer.Close()
	defer smtpServer.Close()

	body, _ := json.Marshal(BatchVerifyRequest{
		Emails: []string{"exists@example.com", "gone@example.com"},
	})
	resp, err := http.Post(httpServer.URL+"/verify/batch", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var batch smtp.BatchVerifyResponse
	json.NewDecoder(resp.Body).Decode(&batch)

	if len(batch.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(batch.Results))
	}
	if batch.Results[0].Result != smtp.ResultDeliverable {
		t.Errorf("expected first result deliverable, got %s", batch.Results[0].Result)
	}
	if batch.Results[1].Result != smtp.ResultUndeliverable {
		t.Errorf("expected second result undeliverable, got %s", batch.Results[1].Result)
	}
	if batch.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", batch.Domain)
	}
}

func TestIntegrationCatchAll(t *testing.T) {
	httpServer, smtpServer := setupIntegrationServer(t, true, nil)
	defer httpServer.Close()
	defer smtpServer.Close()

	body, _ := json.Marshal(VerifyRequest{Email: "anyone@example.com"})
	resp, err := http.Post(httpServer.URL+"/verify", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result smtp.VerifyResult
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Result != smtp.ResultCatchAll {
		t.Errorf("expected catch_all, got %s", result.Result)
	}
	if !result.CatchAll {
		t.Error("expected CatchAll flag to be true")
	}
}

func TestIntegrationHealth(t *testing.T) {
	httpServer, smtpServer := setupIntegrationServer(t, false, nil)
	defer httpServer.Close()
	defer smtpServer.Close()

	resp, err := http.Get(httpServer.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var health HealthResponse
	json.NewDecoder(resp.Body).Decode(&health)
	if health.Status != "ok" {
		t.Errorf("expected status ok, got %s", health.Status)
	}
}

func TestIntegrationNoMX(t *testing.T) {
	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := smtp.NewProber(cfg)
	prober.Resolver = &mockMXResolver{err: &net.DNSError{Err: "no such host", Name: "nonexistent.example.com"}}

	handler := NewHandler(prober)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	body, _ := json.Marshal(VerifyRequest{Email: "user@nonexistent.example.com"})
	resp, err := http.Post(httpServer.URL+"/verify", "application/json", bytes.NewBuffer(body))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result smtp.VerifyResult
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Result != smtp.ResultNoMX {
		t.Errorf("expected no_mx, got %s", result.Result)
	}
}

func TestIntegrationRequestContext(t *testing.T) {
	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := smtp.NewProber(cfg)
	prober.Resolver = &mockMXResolver{err: &net.DNSError{Err: "no such host"}}

	handler := NewHandler(prober)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	body, _ := json.Marshal(VerifyRequest{Email: "user@slow.example.com"})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, httpServer.URL+"/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestVersionEndpoint(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var v version.Info
	json.NewDecoder(w.Body).Decode(&v)
	if v.Version == "" {
		t.Error("expected non-empty version")
	}
}

func TestVersionEndpointWrongMethod(t *testing.T) {
	h := newTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/version", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestAuthMiddlewareNoKeys(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware(nil, inner)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 when no keys configured, got %d", w.Code)
	}
}

func TestAuthMiddlewareValidKey(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware([]string{"secret-key-1", "secret-key-2"}, inner)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-API-Key", "secret-key-2")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 with valid key, got %d", w.Code)
	}
}

func TestAuthMiddlewareInvalidKey(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware([]string{"secret-key-1"}, inner)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 with invalid key, got %d", w.Code)
	}
}

func TestAuthMiddlewareMissingKey(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware([]string{"secret-key-1"}, inner)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401 with missing key, got %d", w.Code)
	}
}

func TestAuthMiddlewareHealthBypass(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware([]string{"secret-key-1"}, inner)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for /health without key, got %d", w.Code)
	}
}

func TestAuthMiddlewareVersionBypass(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := AuthMiddleware([]string{"secret-key-1"}, inner)

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for /version without key, got %d", w.Code)
	}
}
