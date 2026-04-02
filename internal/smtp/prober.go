package smtp

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/sonquer/mailprobe/internal/config"
)

type MXResolver interface {
	LookupMX(ctx context.Context, domain string) ([]*net.MX, error)
}

type NetMXResolver struct{}

func (r *NetMXResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	resolver := &net.Resolver{}
	return resolver.LookupMX(ctx, domain)
}

type Dialer interface {
	DialTimeout(network, address string, timeout time.Duration) (net.Conn, error)
}

type NetDialer struct{}

func (d *NetDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

type Prober struct {
	Config   config.Config
	Resolver MXResolver
	Dialer   Dialer
}

func NewProber(cfg config.Config) *Prober {
	return &Prober{
		Config:   cfg,
		Resolver: &NetMXResolver{},
		Dialer:   &NetDialer{},
	}
}

func (p *Prober) ResolveMX(ctx context.Context, domain string) (string, error) {
	records, err := p.Resolver.LookupMX(ctx, domain)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", fmt.Errorf("no MX records for %s", domain)
	}

	best := records[0]
	for _, r := range records[1:] {
		if r.Pref < best.Pref {
			best = r
		}
	}

	host := strings.TrimSuffix(best.Host, ".")
	return host, nil
}

func (p *Prober) Probe(ctx context.Context, email string) VerifyResult {
	start := time.Now()

	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return VerifyResult{
			Email:      email,
			Result:     ResultUnknown,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}
	domain := parts[1]

	mx, err := p.ResolveMX(ctx, domain)
	if err != nil {
		slog.Debug("MX resolution failed", "domain", domain, "error", err)
		return VerifyResult{
			Email:      email,
			Result:     ResultNoMX,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	conn, err := p.connect(mx)
	if err != nil {
		slog.Debug("SMTP connection failed", "mx", mx, "error", err)
		return VerifyResult{
			Email:      email,
			Result:     ResultUnknown,
			MX:         mx,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}
	defer p.quit(conn)

	if err := p.handshake(conn); err != nil {
		slog.Debug("SMTP handshake failed", "mx", mx, "error", err)
		return VerifyResult{
			Email:      email,
			Result:     ResultUnknown,
			MX:         mx,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	catchAll := p.detectCatchAll(conn, domain)
	if catchAll {
		return VerifyResult{
			Email:      email,
			Result:     ResultCatchAll,
			MX:         mx,
			CatchAll:   true,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	code, err := p.rcptTo(conn, email)
	if err != nil {
		slog.Debug("RCPT TO failed", "email", email, "error", err)
		return VerifyResult{
			Email:      email,
			Result:     ResultUnknown,
			MX:         mx,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	result := ClassifyCode(code)
	return VerifyResult{
		Email:      email,
		Result:     result,
		MX:         mx,
		SMTPCode:   code,
		CatchAll:   false,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

func (p *Prober) ProbeBatch(ctx context.Context, emails []string) BatchVerifyResponse {
	start := time.Now()

	domainGroups := make(map[string][]string)
	for _, email := range emails {
		parts := strings.SplitN(email, "@", 2)
		if len(parts) == 2 && parts[1] != "" {
			domainGroups[parts[1]] = append(domainGroups[parts[1]], email)
		} else {
			domainGroups[""] = append(domainGroups[""], email)
		}
	}

	var allResults []VerifyResult
	var sharedDomain, sharedMX string
	var sharedCatchAll bool

	if len(domainGroups) == 1 {
		for d := range domainGroups {
			sharedDomain = d
		}
	}

	for domain, domainEmails := range domainGroups {
		if domain == "" {
			for _, email := range domainEmails {
				allResults = append(allResults, VerifyResult{
					Email:      email,
					Result:     ResultUnknown,
					DurationMs: 0,
				})
			}
			continue
		}

		results := p.probeDomain(ctx, domain, domainEmails)
		allResults = append(allResults, results...)

		if sharedDomain != "" && len(results) > 0 {
			sharedMX = results[0].MX
			sharedCatchAll = results[0].CatchAll
		}
	}

	resultMap := make(map[string]VerifyResult, len(allResults))
	for _, r := range allResults {
		resultMap[r.Email] = r
	}
	ordered := make([]VerifyResult, 0, len(emails))
	for _, email := range emails {
		if r, ok := resultMap[email]; ok {
			ordered = append(ordered, r)
		}
	}

	return BatchVerifyResponse{
		Results:         ordered,
		Domain:          sharedDomain,
		MX:              sharedMX,
		CatchAll:        sharedCatchAll,
		TotalDurationMs: time.Since(start).Milliseconds(),
	}
}

func (p *Prober) probeDomain(ctx context.Context, domain string, emails []string) []VerifyResult {
	results := make([]VerifyResult, 0, len(emails))

	mx, err := p.ResolveMX(ctx, domain)
	if err != nil {
		slog.Debug("MX resolution failed", "domain", domain, "error", err)
		for _, email := range emails {
			results = append(results, VerifyResult{
				Email:  email,
				Result: ResultNoMX,
			})
		}
		return results
	}

	conn, err := p.connect(mx)
	if err != nil {
		slog.Debug("SMTP connection failed", "mx", mx, "error", err)
		for _, email := range emails {
			results = append(results, VerifyResult{
				Email:  email,
				Result: ResultUnknown,
				MX:     mx,
			})
		}
		return results
	}

	if err := p.handshake(conn); err != nil {
		conn.Close()
		slog.Debug("SMTP handshake failed", "mx", mx, "error", err)
		for _, email := range emails {
			results = append(results, VerifyResult{
				Email:  email,
				Result: ResultUnknown,
				MX:     mx,
			})
		}
		return results
	}

	catchAll := p.detectCatchAll(conn, domain)
	if catchAll {
		p.quit(conn)
		for _, email := range emails {
			results = append(results, VerifyResult{
				Email:    email,
				Result:   ResultCatchAll,
				MX:       mx,
				CatchAll: true,
			})
		}
		return results
	}

	for _, email := range emails {
		probeStart := time.Now()

		code, err := p.rcptTo(conn, email)
		if err != nil {
			slog.Debug("RCPT TO failed, attempting reconnect", "email", email, "error", err)
			conn.Close()

			conn, err = p.connect(mx)
			if err != nil {
				results = append(results, VerifyResult{
					Email:      email,
					Result:     ResultUnknown,
					MX:         mx,
					DurationMs: time.Since(probeStart).Milliseconds(),
				})
				continue
			}

			if err := p.handshake(conn); err != nil {
				conn.Close()
				results = append(results, VerifyResult{
					Email:      email,
					Result:     ResultUnknown,
					MX:         mx,
					DurationMs: time.Since(probeStart).Milliseconds(),
				})
				continue
			}

			code, err = p.rcptTo(conn, email)
			if err != nil {
				results = append(results, VerifyResult{
					Email:      email,
					Result:     ResultUnknown,
					MX:         mx,
					DurationMs: time.Since(probeStart).Milliseconds(),
				})
				continue
			}
		}

		result := ClassifyCode(code)
		results = append(results, VerifyResult{
			Email:      email,
			Result:     result,
			MX:         mx,
			SMTPCode:   code,
			CatchAll:   false,
			DurationMs: time.Since(probeStart).Milliseconds(),
		})

		p.rset(conn)
	}

	p.quit(conn)
	return results
}

func (p *Prober) connect(mx string) (net.Conn, error) {
	addr := mx + ":25"
	return p.Dialer.DialTimeout("tcp", addr, p.Config.SMTPTimeout)
}

func (p *Prober) readResponse(conn net.Conn) (int, string, error) {
	conn.SetReadDeadline(time.Now().Add(p.Config.SMTPTimeout))
	reader := bufio.NewReader(conn)

	var fullResponse string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return 0, fullResponse, err
		}
		fullResponse += line

		if len(line) < 4 {
			break
		}
		if line[3] == ' ' || line[3] == '\n' || line[3] == '\r' {
			break
		}
	}

	if len(fullResponse) < 3 {
		return 0, fullResponse, fmt.Errorf("response too short: %q", fullResponse)
	}

	code, err := strconv.Atoi(fullResponse[:3])
	if err != nil {
		return 0, fullResponse, fmt.Errorf("invalid response code: %q", fullResponse[:3])
	}

	return code, fullResponse, nil
}

func (p *Prober) sendCommand(conn net.Conn, cmd string) error {
	conn.SetWriteDeadline(time.Now().Add(p.Config.SMTPTimeout))
	_, err := fmt.Fprintf(conn, "%s\r\n", cmd)
	return err
}

func (p *Prober) handshake(conn net.Conn) error {
	code, _, err := p.readResponse(conn)
	if err != nil {
		return fmt.Errorf("reading greeting: %w", err)
	}
	if code != 220 {
		return fmt.Errorf("unexpected greeting code: %d", code)
	}

	if err := p.sendCommand(conn, "EHLO "+p.Config.HELODomain); err != nil {
		return fmt.Errorf("sending EHLO: %w", err)
	}
	code, _, err = p.readResponse(conn)
	if err != nil {
		return fmt.Errorf("reading EHLO response: %w", err)
	}
	if code != 250 {
		if err := p.sendCommand(conn, "HELO "+p.Config.HELODomain); err != nil {
			return fmt.Errorf("sending HELO: %w", err)
		}
		code, _, err = p.readResponse(conn)
		if err != nil {
			return fmt.Errorf("reading HELO response: %w", err)
		}
		if code != 250 {
			return fmt.Errorf("HELO rejected with code: %d", code)
		}
	}

	if err := p.sendCommand(conn, "MAIL FROM:<"+p.Config.MailFrom+">"); err != nil {
		return fmt.Errorf("sending MAIL FROM: %w", err)
	}
	code, _, err = p.readResponse(conn)
	if err != nil {
		return fmt.Errorf("reading MAIL FROM response: %w", err)
	}
	if code != 250 {
		return fmt.Errorf("MAIL FROM rejected with code: %d", code)
	}

	return nil
}

func (p *Prober) rcptTo(conn net.Conn, email string) (int, error) {
	if err := p.sendCommand(conn, "RCPT TO:<"+email+">"); err != nil {
		return 0, err
	}
	code, _, err := p.readResponse(conn)
	if err != nil {
		return 0, err
	}
	return code, nil
}

func (p *Prober) rset(conn net.Conn) {
	if err := p.sendCommand(conn, "RSET"); err != nil {
		return
	}
	p.readResponse(conn)
}

func (p *Prober) quit(conn net.Conn) {
	p.sendCommand(conn, "QUIT")
	conn.Close()
}

func (p *Prober) detectCatchAll(conn net.Conn, domain string) bool {
	randomUser := GenerateRandomUser()
	randomEmail := randomUser + "@" + domain

	code, err := p.rcptTo(conn, randomEmail)
	if err != nil {
		return false
	}

	p.rset(conn)

	return code == 250
}

func GenerateRandomUser() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("zxqj_%x", b)
}

func ClassifyCode(code int) string {
	switch {
	case code == 250:
		return ResultDeliverable
	case code == 550 || code == 551 || code == 552 || code == 553:
		return ResultUndeliverable
	default:
		return ResultUnknown
	}
}
