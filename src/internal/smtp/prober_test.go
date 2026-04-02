package smtp

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sonquer/mailprobe/internal/config"
)

type mockSMTPServer struct {
	listener net.Listener
	catchAll bool
}

const (
	stateGreeting = iota
	stateEhlo
	stateMailFrom
	stateRcptTo
)

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

	state := stateGreeting
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
			if state != stateGreeting {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateEhlo
			fmt.Fprintf(writer, "250-mock.smtp.server\r\n250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "HELO"):
			if state != stateGreeting && state != stateEhlo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateEhlo
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if state != stateEhlo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateMailFrom
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if state != stateMailFrom && state != stateRcptTo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateRcptTo
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
			state = stateEhlo
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

func TestProbeBatchManyVariantsBruteForce(t *testing.T) {
	accepted := make(map[string]int)
	var emails []string
	for i := 0; i < 20; i++ {
		email := fmt.Sprintf("john.doe.variant%d@example.com", i)
		if i%3 == 0 {
			accepted[email] = 250
		}
		emails = append(emails, email)
	}

	server := newMockSMTPServer(t, false, accepted)
	defer server.Close()

	cfg := config.Config{SMTPTimeout: 5 * time.Second, HELODomain: "localhost", MailFrom: "probe@localhost"}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	resp := prober.ProbeBatch(context.Background(), emails)

	if len(resp.Results) != 20 {
		t.Fatalf("expected 20 results, got %d", len(resp.Results))
	}

	for i, r := range resp.Results {
		expectedEmail := fmt.Sprintf("john.doe.variant%d@example.com", i)
		if r.Email != expectedEmail {
			t.Errorf("result %d: expected email %s, got %s", i, expectedEmail, r.Email)
		}
		if i%3 == 0 {
			if r.Result != ResultDeliverable {
				t.Errorf("result %d (%s): expected deliverable, got %s", i, r.Email, r.Result)
			}
		} else {
			if r.Result != ResultUndeliverable {
				t.Errorf("result %d (%s): expected undeliverable, got %s", i, r.Email, r.Result)
			}
		}
	}

	if resp.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", resp.Domain)
	}
}

type transientMockServer struct {
	listener     net.Listener
	rcptAttempts atomic.Int64
	transientN   int
	accepted     map[string]int
}

func newTransientMockServer(t *testing.T, transientN int, accepted map[string]int) *transientMockServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start transient mock server: %v", err)
	}

	s := &transientMockServer{
		listener:   listener,
		transientN: transientN,
		accepted:   accepted,
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.handleConnection(conn)
		}
	}()

	return s
}

func (s *transientMockServer) handleConnection(conn net.Conn) {
	defer conn.Close()
	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	state := stateGreeting
	fmt.Fprintf(writer, "220 transient.mock ESMTP\r\n")
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
			if state != stateGreeting {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateEhlo
			fmt.Fprintf(writer, "250-transient.mock\r\n250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if state != stateEhlo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateMailFrom
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if state != stateMailFrom && state != stateRcptTo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateRcptTo
			email := extractEmail(line)

			if strings.HasPrefix(email, "zxqj_") {
				fmt.Fprintf(writer, "550 User not found\r\n")
				writer.Flush()
				continue
			}

			attempt := s.rcptAttempts.Add(1)
			if int(attempt) <= s.transientN {
				fmt.Fprintf(writer, "450 Try again later\r\n")
				writer.Flush()
				continue
			}

			if code, ok := s.accepted[email]; ok {
				fmt.Fprintf(writer, "%d OK\r\n", code)
			} else {
				fmt.Fprintf(writer, "550 User not found\r\n")
			}
			writer.Flush()
		case strings.HasPrefix(cmd, "RSET"):
			state = stateEhlo
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

func (s *transientMockServer) Addr() string {
	return s.listener.Addr().String()
}

func (s *transientMockServer) Close() {
	s.listener.Close()
}

func TestProbeRetriesTransient450(t *testing.T) {
	accepted := map[string]int{"user@example.com": 250}
	server := newTransientMockServer(t, 1, accepted)
	defer server.Close()

	cfg := config.Config{
		SMTPTimeout: 5 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
		MaxRetries:  2,
		RetryDelay:  10 * time.Millisecond,
	}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultDeliverable {
		t.Errorf("expected deliverable after retry, got %s (code %d)", result.Result, result.SMTPCode)
	}
	if result.SMTPCode != 250 {
		t.Errorf("expected SMTP code 250, got %d", result.SMTPCode)
	}
}

func TestProbeRetriesExhausted(t *testing.T) {
	accepted := map[string]int{"user@example.com": 250}
	server := newTransientMockServer(t, 100, accepted)
	defer server.Close()

	cfg := config.Config{
		SMTPTimeout: 5 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
		MaxRetries:  2,
		RetryDelay:  10 * time.Millisecond,
	}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultUnknown {
		t.Errorf("expected unknown after exhausted retries, got %s", result.Result)
	}
}

type dropAfterNMockServer struct {
	listener  net.Listener
	dropAfter int
	rcptCount atomic.Int64
	mu        sync.Mutex
	accepted  map[string]int
}

func newDropAfterNMockServer(t *testing.T, dropAfter int, accepted map[string]int) *dropAfterNMockServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start drop-after-N mock server: %v", err)
	}

	s := &dropAfterNMockServer{
		listener:  listener,
		dropAfter: dropAfter,
		accepted:  accepted,
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.handleConnection(conn)
		}
	}()

	return s
}

func (s *dropAfterNMockServer) handleConnection(conn net.Conn) {
	defer conn.Close()
	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	state := stateGreeting
	fmt.Fprintf(writer, "220 drop.mock ESMTP\r\n")
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
			if state != stateGreeting {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateEhlo
			fmt.Fprintf(writer, "250-drop.mock\r\n250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if state != stateEhlo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateMailFrom
			fmt.Fprintf(writer, "250 OK\r\n")
			writer.Flush()
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if state != stateMailFrom && state != stateRcptTo {
				fmt.Fprintf(writer, "503 Bad sequence of commands\r\n")
				writer.Flush()
				continue
			}
			state = stateRcptTo
			email := extractEmail(line)

			if strings.HasPrefix(email, "zxqj_") {
				fmt.Fprintf(writer, "550 User not found\r\n")
				writer.Flush()
				continue
			}

			count := s.rcptCount.Add(1)
			if int(count) == s.dropAfter+1 {
				return
			}

			if code, ok := s.accepted[email]; ok {
				fmt.Fprintf(writer, "%d OK\r\n", code)
			} else {
				fmt.Fprintf(writer, "550 User not found\r\n")
			}
			writer.Flush()
		case strings.HasPrefix(cmd, "RSET"):
			state = stateEhlo
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

func (s *dropAfterNMockServer) Addr() string {
	return s.listener.Addr().String()
}

func (s *dropAfterNMockServer) Close() {
	s.listener.Close()
}

func TestProbeBatchReconnectsAfterDrop(t *testing.T) {
	accepted := make(map[string]int)
	var emails []string
	for i := 0; i < 6; i++ {
		email := fmt.Sprintf("user%d@example.com", i)
		accepted[email] = 250
		emails = append(emails, email)
	}

	server := newDropAfterNMockServer(t, 3, accepted)
	defer server.Close()

	cfg := config.Config{
		SMTPTimeout: 5 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
		MaxRetries:  2,
		RetryDelay:  10 * time.Millisecond,
	}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	resp := prober.ProbeBatch(context.Background(), emails)

	if len(resp.Results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(resp.Results))
	}

	deliverableCount := 0
	for _, r := range resp.Results {
		if r.Result == ResultDeliverable {
			deliverableCount++
		}
		if r.Result != ResultDeliverable && r.Result != ResultUnknown {
			t.Errorf("unexpected result for %s: %s", r.Email, r.Result)
		}
	}

	if deliverableCount < 3 {
		t.Errorf("expected at least 3 deliverable results (before drop), got %d", deliverableCount)
	}

	for _, r := range resp.Results {
		if r.Email != emails[0] && r.Email != emails[1] && r.Email != emails[2] &&
			r.Email != emails[3] && r.Email != emails[4] && r.Email != emails[5] {
			t.Errorf("unexpected email in results: %s", r.Email)
		}
	}
}

func TestProbeBatchTransientThenRecovers(t *testing.T) {
	accepted := make(map[string]int)
	var emails []string
	for i := 0; i < 5; i++ {
		email := fmt.Sprintf("test%d@example.com", i)
		accepted[email] = 250
		emails = append(emails, email)
	}

	server := newTransientMockServer(t, 2, accepted)
	defer server.Close()

	cfg := config.Config{
		SMTPTimeout: 5 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
		MaxRetries:  3,
		RetryDelay:  10 * time.Millisecond,
	}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, &mockDialer{addr: server.Addr()})

	resp := prober.ProbeBatch(context.Background(), emails)

	if len(resp.Results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(resp.Results))
	}

	for _, r := range resp.Results {
		if r.Result != ResultDeliverable && r.Result != ResultUnknown {
			t.Errorf("result for %s: expected deliverable or unknown, got %s", r.Email, r.Result)
		}
	}

	deliverableCount := 0
	for _, r := range resp.Results {
		if r.Result == ResultDeliverable {
			deliverableCount++
		}
	}
	if deliverableCount == 0 {
		t.Error("expected at least some deliverable results after transient recovery")
	}
}

func TestProbeRetryOnConnectionRefused(t *testing.T) {
	accepted := map[string]int{"user@example.com": 250}
	server := newMockSMTPServer(t, false, accepted)
	goodAddr := server.Addr()

	badListener, _ := net.Listen("tcp", "127.0.0.1:0")
	badAddr := badListener.Addr().String()
	badListener.Close()

	callCount := atomic.Int64{}

	dialer := &retryDialer{
		goodAddr:  goodAddr,
		badAddr:   badAddr,
		failFirst: 1,
		calls:     &callCount,
	}

	cfg := config.Config{
		SMTPTimeout: 2 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
		MaxRetries:  2,
		RetryDelay:  10 * time.Millisecond,
	}
	prober := newTestProber(cfg, &mockMXResolver{host: "127.0.0.1."}, dialer)

	result := prober.Probe(context.Background(), "user@example.com")

	if result.Result != ResultDeliverable {
		t.Errorf("expected deliverable after connection retry, got %s", result.Result)
	}

	server.Close()
}

type retryDialer struct {
	goodAddr  string
	badAddr   string
	failFirst int
	calls     *atomic.Int64
}

func (d *retryDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	n := d.calls.Add(1)
	if int(n) <= d.failFirst {
		return net.DialTimeout(network, d.badAddr, timeout)
	}
	return net.DialTimeout(network, d.goodAddr, timeout)
}

func TestConfigMaxRetriesAndDelay(t *testing.T) {
	t.Setenv("MAX_RETRIES", "5")
	t.Setenv("RETRY_DELAY", "2s")

	cfg := config.Load()
	if cfg.MaxRetries != 5 {
		t.Errorf("expected MaxRetries 5, got %d", cfg.MaxRetries)
	}
	if cfg.RetryDelay != 2*time.Second {
		t.Errorf("expected RetryDelay 2s, got %v", cfg.RetryDelay)
	}
}

func TestConfigMaxRetriesDefault(t *testing.T) {
	cfg := config.Load()
	if cfg.MaxRetries != 2 {
		t.Errorf("expected default MaxRetries 2, got %d", cfg.MaxRetries)
	}
	if cfg.RetryDelay != 1*time.Second {
		t.Errorf("expected default RetryDelay 1s, got %v", cfg.RetryDelay)
	}
}

func TestConfigMaxRetriesInvalid(t *testing.T) {
	t.Setenv("MAX_RETRIES", "abc")
	cfg := config.Load()
	if cfg.MaxRetries != 2 {
		t.Errorf("expected default MaxRetries 2 for invalid value, got %d", cfg.MaxRetries)
	}
}
