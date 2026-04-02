package api

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sonquer/mailprobe/internal/config"
	"github.com/sonquer/mailprobe/internal/smtp"
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

func setupIntegrationServer(t *testing.T, catchAll bool, acceptedEmails map[string]int) (*httptest.Server, *mockSMTPServer) {
	t.Helper()

	smtpServer := newMockSMTPServer(t, catchAll, acceptedEmails)

	cfg := config.Config{
		SMTPTimeout: 5 * time.Second,
		HELODomain:  "localhost",
		MailFrom:    "probe@localhost",
	}

	prober := smtp.NewProber(cfg)
	prober.Resolver = &mockMXResolver{host: "127.0.0.1."}
	prober.Dialer = &mockDialer{addr: smtpServer.Addr()}

	handler := NewHandler(prober)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	httpServer := httptest.NewServer(mux)

	return httpServer, smtpServer
}
