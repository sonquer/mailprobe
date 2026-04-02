package smtp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sonquer/mailprobe/internal/config"
)

type mockSMTPServer struct {
	listener net.Listener
	catchAll bool
}

func newMockSMTPServer(t *testing.T, catchAll bool, acceptedEmails map[string]int) *mockSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start mock SMTP server: %v", err)
	}

	s := &mockSMTPServer{
		listener: listener,
		catchAll: catchAll,
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.handleConnection(conn, acceptedEmails)
		}
	}()

	return s
}

func (s *mockSMTPServer) handleConnection(conn net.Conn, acceptedEmails map[string]int) {
	defer conn.Close()
	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	fmt.Fprintf(writer, "220 mock.smtp.server ESMTP\r\n")
	writer.Flush()

	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		cmd := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			fmt.Fprintf(writer, "250-mock.smtp.server\r\n250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "HELO"):
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "RCPT TO:"):
			email := extractEmail(line)

			if s.catchAll {
				fmt.Fprintf(writer, "250 OK\r\n")
				writer.Flush()
				continue
			}

			if code, ok := acceptedEmails[email]; ok {
				fmt.Fprintf(writer, "%d OK\r\n", code)
			} else {
				fmt.Fprintf(writer, "550 User not found\r\n")
			}
			writer.Flush()
		case strings.HasPrefix(cmd, "RSET"):
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "QUIT"):
			fmt.Fprintf(writer, "221 Bye\r\n")
			writer.Flush()
			return
		default:
			fmt.Fprintf(writer, "500 Unknown command\r\n")
			writer.Flush()
		}
	}
}

func extractEmail(rcptLine string) string {
	start := strings.Index(rcptLine, "<")
	end := strings.Index(rcptLine, ">")
	if start >= 0 && end > start {
		return rcptLine[start+1 : end]
	}
	return ""
}

func (s *mockSMTPServer) Addr() string {
	return s.listener.Addr().String()
}

func (s *mockSMTPServer) Close() {
	s.listener.Close()
}

type mockMXResolver struct {
	host string
	err  error
}

func (m *mockMXResolver) LookupMX(_ context.Context, _ string) ([]*net.MX, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []*net.MX{{Host: m.host, Pref: 10}}, nil
}

type mockDialer struct {
	addr string
}

func (d *mockDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, d.addr, timeout)
}

func newTestProber(cfg config.Config, resolver MXResolver, dialer Dialer) *Prober {
	p := NewProber(cfg)
	p.Resolver = resolver
	p.Dialer = dialer
	return p
}

func TestProbeDeliverable(t *testing.T) {
	accepted := map[string]int{"user@example.com": 250}
	server := newMockSMTPServer(t, false, accepted)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultDeliverable {
		t.Errorf("expected deliverable, got %s", result.Result)
	}
	if result.SMTPCode != 250 {
		t.Errorf("expected SMTP code 250, got %d", result.SMTPCode)
	}
	if result.Email != "user@example.com" {
		t.Errorf("expected email user@example.com, got %s", result.Email)
	}
}

func TestProbeUndeliverable(t *testing.T) {
	server := newMockSMTPServer(t, false, nil)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	result := prober.Probe(context.Background(), "nonexistent@example.com")

	if result.Result != ResultUndeliverable {
		t.Errorf("expected undeliverable, got %s", result.Result)
	}
	if result.SMTPCode != 550 {
		t.Errorf("expected SMTP code 550, got %d", result.SMTPCode)
	}
}

func TestProbeCatchAll(t *testing.T) {
	server := newMockSMTPServer(t, true, nil)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	result := prober.Probe(context.Background(), "anyone@example.com")

	if result.Result != ResultCatchAll {
		t.Errorf("expected catch_all, got %s", result.Result)
	}
	if !result.CatchAll {
		t.Error("expected CatchAll flag to be true")
	}
}

func TestProbeNoMX(t *testing.T) {
	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{err: fmt.Errorf("no such host")}, nil)

	result := prober.Probe(context.Background(), "user@nonexistent.example.com")

	if result.Result != ResultNoMX {
		t.Errorf("expected no_mx, got %s", result.Result)
	}
}

func TestProbeConnectionRefused(t *testing.T) {
	listener, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := listener.Addr().String()
	listener.Close()

	cfg := config.Config{SMTPTimeout: 2 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: addr})

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultUnknown {
		t.Errorf("expected unknown, got %s", result.Result)
	}
}

func TestProbeInvalidEmail(t *testing.T) {
	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := NewProber(cfg)

	result := prober.Probe(context.Background(), "notanemail")

	if result.Result != ResultUnknown {
		t.Errorf("expected unknown, got %s", result.Result)
	}
}

func TestProbeGreylisting(t *testing.T) {
	accepted := map[string]int{"user@example.com": 450}
	server := newMockSMTPServer(t, false, accepted)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultUnknown {
		t.Errorf("expected unknown for greylisting, got %s", result.Result)
	}
	if result.SMTPCode != 450 {
		t.Errorf("expected SMTP code 450, got %d", result.SMTPCode)
	}
}

func TestProbeBatchSameDomain(t *testing.T) {
	accepted := map[string]int{
		"user1@example.com": 250,
		"user2@example.com": 250,
	}
	server := newMockSMTPServer(t, false, accepted)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	emails := []string{"user1@example.com", "user2@example.com", "user3@example.com"}
	resp := prober.ProbeBatch(context.Background(), emails)

	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}

	if resp.Results[0].Result != ResultDeliverable {
		t.Errorf("expected user1 deliverable, got %s", resp.Results[0].Result)
	}
	if resp.Results[1].Result != ResultDeliverable {
		t.Errorf("expected user2 deliverable, got %s", resp.Results[1].Result)
	}
	if resp.Results[2].Result != ResultUndeliverable {
		t.Errorf("expected user3 undeliverable, got %s", resp.Results[2].Result)
	}

	if resp.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", resp.Domain)
	}
}

func TestProbeBatchCatchAll(t *testing.T) {
	server := newMockSMTPServer(t, true, nil)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	emails := []string{"a@example.com", "b@example.com"}
	resp := prober.ProbeBatch(context.Background(), emails)

	for _, r := range resp.Results {
		if r.Result != ResultCatchAll {
			t.Errorf("expected catch_all for %s, got %s", r.Email, r.Result)
		}
	}
	if !resp.CatchAll {
		t.Error("expected batch CatchAll to be true")
	}
}

func TestProbeBatchPreservesOrder(t *testing.T) {
	accepted := map[string]int{
		"c@example.com": 250,
		"a@example.com": 250,
	}
	server := newMockSMTPServer(t, false, accepted)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	emails := []string{"c@example.com", "b@example.com", "a@example.com"}
	resp := prober.ProbeBatch(context.Background(), emails)

	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(resp.Results))
	}
	if resp.Results[0].Email != "c@example.com" {
		t.Errorf("expected first result c@example.com, got %s", resp.Results[0].Email)
	}
	if resp.Results[1].Email != "b@example.com" {
		t.Errorf("expected second result b@example.com, got %s", resp.Results[1].Email)
	}
	if resp.Results[2].Email != "a@example.com" {
		t.Errorf("expected third result a@example.com, got %s", resp.Results[2].Email)
	}
}

func TestClassifyCode(t *testing.T) {
	tests := []struct {
		code     int
		expected string
	}{
		{250, ResultDeliverable},
		{550, ResultUndeliverable},
		{551, ResultUndeliverable},
		{552, ResultUndeliverable},
		{553, ResultUndeliverable},
		{450, ResultUnknown},
		{451, ResultUnknown},
		{452, ResultUnknown},
		{421, ResultUnknown},
		{500, ResultUnknown},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("code_%d", tt.code), func(t *testing.T) {
			result := ClassifyCode(tt.code)
			if result != tt.expected {
				t.Errorf("expected %s for code %d, got %s", tt.expected, tt.code, result)
			}
		})
	}
}

func TestGenerateRandomUser(t *testing.T) {
	u1 := GenerateRandomUser()
	u2 := GenerateRandomUser()

	if !strings.HasPrefix(u1, "zxqj_") {
		t.Errorf("expected prefix zxqj_, got %s", u1)
	}
	if u1 == u2 {
		t.Error("expected different random users, got identical")
	}
}

func TestResolveMXTrimsTrailingDot(t *testing.T) {
	cfg := config.Config{SMTPTimeout: 5 * time.Second}
	prober := newTestProber(cfg, &mockMXResolver{host: "mx.example.com."}, nil)

	mx, err := prober.ResolveMX(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mx != "mx.example.com" {
		t.Errorf("expected mx.example.com (no trailing dot), got %s", mx)
	}
}

func TestProbeTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			time.Sleep(10 * time.Second)
			conn.Close()
		}
	}()

	cfg := config.Config{SMTPTimeout: 1 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: listener.Addr().String()})

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultUnknown {
		t.Errorf("expected unknown for timeout, got %s", result.Result)
	}
}

func TestProbeMultipleSMTPCodes(t *testing.T) {
	tests := []struct {
		code     int
		expected string
	}{
		{250, ResultDeliverable},
		{550, ResultUndeliverable},
		{551, ResultUndeliverable},
		{552, ResultUndeliverable},
		{553, ResultUndeliverable},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("smtp_%d", tt.code), func(t *testing.T) {
			accepted := map[string]int{"user@example.com": tt.code}
			server := newMockSMTPServer(t, false, accepted)
			defer server.Close()

			cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
			prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

			result := prober.Probe(context.Background(), "user@example.com")

			if result.Result != tt.expected {
				t.Errorf("expected %s for SMTP %d, got %s", tt.expected, tt.code, result.Result)
			}
		})
	}
}
