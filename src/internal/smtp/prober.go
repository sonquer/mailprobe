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

// MXResolver resolves MX records for a given domain.
type MXResolver interface {
	LookupMX(ctx context.Context, domain string) ([]*net.MX, error)
}

// NetMXResolver implements MXResolver using the standard library DNS resolver.
type NetMXResolver struct{}

// LookupMX resolves MX records for the given domain using the system DNS resolver.
func (r *NetMXResolver) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	resolver := &net.Resolver{}
	return resolver.LookupMX(ctx, domain)
}

// Dialer establishes TCP connections with a timeout.
type Dialer interface {
	DialTimeout(network, address string, timeout time.Duration) (net.Conn, error)
}

// NetDialer implements Dialer using the standard library net package.
type NetDialer struct{}

// DialTimeout opens a TCP connection to the given address with a timeout.
func (d *NetDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

type smtpConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func newSMTPConn(conn net.Conn) *smtpConn {
	return &smtpConn{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}
}

func (sc *smtpConn) Close() {
	sc.conn.Close()
}

// Prober performs SMTP RCPT TO probing to verify email addresses. It uses
// dependency-injected MXResolver and Dialer to allow testing without network access.
type Prober struct {
	Config   config.Config
	Resolver MXResolver
	Dialer   Dialer
}

// NewProber creates a Prober with production MX resolver and TCP dialer.
func NewProber(cfg config.Config) *Prober {
	return &Prober{
		Config:   cfg,
		Resolver: &NetMXResolver{},
		Dialer:   &NetDialer{},
	}
}

// ResolveMX returns the hostname of the highest-priority MX record for the given domain.
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

func (p *Prober) isTransient(code int) bool {
	return code >= 400 && code < 500
}

func (p *Prober) connectAndHandshake(mx string) (*smtpConn, error) {
	var lastErr error
	maxAttempts := 1 + p.Config.MaxRetries

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			slog.Debug("retrying connect+handshake", "mx", mx, "attempt", attempt+1)
			time.Sleep(p.Config.RetryDelay)
		}

		sc, err := p.connect(mx)
		if err != nil {
			lastErr = err
			continue
		}

		if err := p.handshake(sc); err != nil {
			sc.Close()
			lastErr = err
			continue
		}

		return sc, nil
	}

	return nil, lastErr
}

func (p *Prober) reconnect(mx string) (*smtpConn, error) {
	sc, err := p.connect(mx)
	if err != nil {
		return nil, err
	}
	if err := p.handshake(sc); err != nil {
		sc.Close()
		return nil, err
	}
	return sc, nil
}

// Probe verifies a single email address by connecting to the domain's MX server,
// performing an SMTP handshake, checking for catch-all, and issuing RCPT TO.
// Transient failures (4xx codes, connection errors) are retried up to MaxRetries times.
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

	maxAttempts := 1 + p.Config.MaxRetries
	var lastCode int
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(p.Config.RetryDelay)
			slog.Debug("retrying single probe", "email", email, "attempt", attempt+1)
		}

		sc, connErr := p.connectAndHandshake(mx)
		if connErr != nil {
			lastErr = connErr
			continue
		}

		catchAll := p.detectCatchAll(sc, domain)
		if catchAll {
			p.quit(sc)
			return VerifyResult{
				Email:      email,
				Result:     ResultCatchAll,
				MX:         mx,
				CatchAll:   true,
				DurationMs: time.Since(start).Milliseconds(),
			}
		}

		code, rcptErr := p.rcptTo(sc, email)
		p.quit(sc)

		if rcptErr != nil {
			lastErr = rcptErr
			continue
		}

		if p.isTransient(code) {
			lastCode = code
			slog.Debug("transient RCPT TO response", "email", email, "code", code, "attempt", attempt+1)
			continue
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

	if lastErr != nil {
		slog.Debug("probe failed after all retries", "email", email, "error", lastErr)
		return VerifyResult{
			Email:      email,
			Result:     ResultUnknown,
			MX:         mx,
			DurationMs: time.Since(start).Milliseconds(),
		}
	}

	return VerifyResult{
		Email:      email,
		Result:     ResultUnknown,
		MX:         mx,
		SMTPCode:   lastCode,
		DurationMs: time.Since(start).Milliseconds(),
	}
}

// ProbeBatch verifies multiple email addresses, grouping them by domain to reuse
// SMTP connections and MX resolution. Results are returned in the original input order.
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

	sc, err := p.connectAndHandshake(mx)
	if err != nil {
		slog.Debug("SMTP connect+handshake failed after retries", "mx", mx, "error", err)
		for _, email := range emails {
			results = append(results, VerifyResult{
				Email:  email,
				Result: ResultUnknown,
				MX:     mx,
			})
		}
		return results
	}

	catchAll := p.detectCatchAll(sc, domain)
	if catchAll {
		p.quit(sc)
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

	for i, email := range emails {
		probeStart := time.Now()

		code, err := p.rcptTo(sc, email)

		needsRetry := err != nil || p.isTransient(code)

		if needsRetry {
			if err != nil {
				slog.Debug("RCPT TO failed in batch", "email", email, "error", err)
			} else {
				slog.Debug("RCPT TO transient response in batch", "email", email, "code", code)
			}

			retried := false
			for attempt := 0; attempt < p.Config.MaxRetries; attempt++ {
				time.Sleep(p.Config.RetryDelay)
				slog.Debug("retrying probe in batch", "email", email, "attempt", attempt+2)

				sc.Close()
				sc, err = p.reconnect(mx)
				if err != nil {
					slog.Debug("reconnect failed during batch retry", "mx", mx, "error", err)
					continue
				}

				code, err = p.rcptTo(sc, email)
				if err != nil {
					slog.Debug("RCPT TO failed after reconnect in batch", "email", email, "error", err)
					continue
				}

				if !p.isTransient(code) {
					retried = true
					break
				}
			}

			if !retried {
				if err != nil {
					results = append(results, VerifyResult{
						Email:      email,
						Result:     ResultUnknown,
						MX:         mx,
						DurationMs: time.Since(probeStart).Milliseconds(),
					})
				} else {
					results = append(results, VerifyResult{
						Email:      email,
						Result:     ResultUnknown,
						MX:         mx,
						SMTPCode:   code,
						DurationMs: time.Since(probeStart).Milliseconds(),
					})
				}

				if err != nil {
					sc, err = p.reconnect(mx)
					if err != nil {
						slog.Debug("reconnect after exhausted retries failed", "mx", mx, "error", err)
						for _, remaining := range emails[i+1:] {
							results = append(results, VerifyResult{
								Email:  remaining,
								Result: ResultUnknown,
								MX:     mx,
							})
						}
						return results
					}
				}

				if i < len(emails)-1 {
					if rsetErr := p.rset(sc); rsetErr != nil {
						slog.Debug("RSET failed after exhausted retries", "error", rsetErr)
						sc.Close()
						sc, err = p.reconnect(mx)
						if err != nil {
							for _, remaining := range emails[i+1:] {
								results = append(results, VerifyResult{
									Email:  remaining,
									Result: ResultUnknown,
									MX:     mx,
								})
							}
							return results
						}
					}
				}

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

		if i < len(emails)-1 {
			if rsetErr := p.rset(sc); rsetErr != nil {
				slog.Debug("RSET failed, reconnecting", "email", email, "error", rsetErr)
				sc.Close()

				sc, err = p.reconnect(mx)
				if err != nil {
					slog.Debug("reconnect after RSET failure failed", "mx", mx, "error", err)
					for _, remaining := range emails[i+1:] {
						results = append(results, VerifyResult{
							Email:  remaining,
							Result: ResultUnknown,
							MX:     mx,
						})
					}
					return results
				}
			}
		}
	}

	p.quit(sc)
	return results
}

func (p *Prober) connect(mx string) (*smtpConn, error) {
	addr := mx + ":25"
	conn, err := p.Dialer.DialTimeout("tcp", addr, p.Config.SMTPTimeout)
	if err != nil {
		return nil, err
	}
	return newSMTPConn(conn), nil
}

func (p *Prober) readResponse(sc *smtpConn) (int, string, error) {
	sc.conn.SetReadDeadline(time.Now().Add(p.Config.SMTPTimeout))

	var fullResponse string
	for {
		line, err := sc.reader.ReadString('\n')
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

func (p *Prober) sendCommand(sc *smtpConn, cmd string) error {
	sc.conn.SetWriteDeadline(time.Now().Add(p.Config.SMTPTimeout))
	_, err := fmt.Fprintf(sc.conn, "%s\r\n", cmd)
	return err
}

func (p *Prober) handshake(sc *smtpConn) error {
	code, _, err := p.readResponse(sc)
	if err != nil {
		return fmt.Errorf("reading greeting: %w", err)
	}
	if code != 220 {
		return fmt.Errorf("unexpected greeting code: %d", code)
	}

	if err := p.sendCommand(sc, "EHLO "+p.Config.HELODomain); err != nil {
		return fmt.Errorf("sending EHLO: %w", err)
	}
	code, _, err = p.readResponse(sc)
	if err != nil {
		return fmt.Errorf("reading EHLO response: %w", err)
	}
	if code != 250 {
		if err := p.sendCommand(sc, "HELO "+p.Config.HELODomain); err != nil {
			return fmt.Errorf("sending HELO: %w", err)
		}
		code, _, err = p.readResponse(sc)
		if err != nil {
			return fmt.Errorf("reading HELO response: %w", err)
		}
		if code != 250 {
			return fmt.Errorf("HELO rejected with code: %d", code)
		}
	}

	if err := p.sendCommand(sc, "MAIL FROM:<"+p.Config.MailFrom+">"); err != nil {
		return fmt.Errorf("sending MAIL FROM: %w", err)
	}
	code, _, err = p.readResponse(sc)
	if err != nil {
		return fmt.Errorf("reading MAIL FROM response: %w", err)
	}
	if code != 250 {
		return fmt.Errorf("MAIL FROM rejected with code: %d", code)
	}

	return nil
}

func (p *Prober) rcptTo(sc *smtpConn, email string) (int, error) {
	if err := p.sendCommand(sc, "RCPT TO:<"+email+">"); err != nil {
		return 0, err
	}
	code, _, err := p.readResponse(sc)
	if err != nil {
		return 0, err
	}
	return code, nil
}

func (p *Prober) rset(sc *smtpConn) error {
	if err := p.sendCommand(sc, "RSET"); err != nil {
		return err
	}
	code, _, err := p.readResponse(sc)
	if err != nil {
		return err
	}
	if code != 250 {
		return fmt.Errorf("RSET rejected with code: %d", code)
	}

	if err := p.sendCommand(sc, "MAIL FROM:<"+p.Config.MailFrom+">"); err != nil {
		return err
	}
	code, _, err = p.readResponse(sc)
	if err != nil {
		return err
	}
	if code != 250 {
		return fmt.Errorf("MAIL FROM after RSET rejected with code: %d", code)
	}

	return nil
}

func (p *Prober) quit(sc *smtpConn) {
	p.sendCommand(sc, "QUIT")
	sc.Close()
}

func (p *Prober) detectCatchAll(sc *smtpConn, domain string) bool {
	randomUser := GenerateRandomUser()
	randomEmail := randomUser + "@" + domain

	code, err := p.rcptTo(sc, randomEmail)
	if err != nil {
		return false
	}

	isCatchAll := code == 250

	if err := p.rset(sc); err != nil {
		slog.Debug("RSET after catch-all detection failed", "domain", domain, "error", err)
	}

	return isCatchAll
}

// GenerateRandomUser returns a random username for catch-all detection probes.
func GenerateRandomUser() string {
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("zxqj_%x", b)
}

// ClassifyCode maps an SMTP response code to a verification result string.
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
