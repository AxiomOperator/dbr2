// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTP TLS modes.
const (
	TLSStartTLS = "starttls"
	TLSImplicit = "tls"
	TLSNone     = "none"
)

// Email timeouts: connecting, and the whole SMTP conversation.
const (
	SMTPDialTimeout = 10 * time.Second
	SMTPTimeout     = 30 * time.Second
)

// ErrSMTPNotConfigured means email channels cannot send yet.
var ErrSMTPNotConfigured = errors.New("SMTP is not configured (System → Settings → Email)")

// SMTPConfig is the stored SMTP relay configuration (platform_settings key
// "smtp"); the password is stored sealed, never in Value.
type SMTPConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	From     string `json:"from"`
	TLS      string `json:"tls"`
	Password string `json:"-"`
}

// Validate checks the configuration. TLS "none" (plain text) is allowed only
// for a relay on localhost.
func (c SMTPConfig) Validate() error {
	if strings.TrimSpace(c.Host) == "" || strings.ContainsAny(c.Host, " /\r\n") {
		return fmt.Errorf("%w: SMTP host is required", ErrInvalid)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: SMTP port must be 1-65535", ErrInvalid)
	}
	if _, err := parseAddress(c.From); err != nil {
		return fmt.Errorf("%w: from: %v", ErrInvalid, err)
	}
	switch c.TLS {
	case TLSStartTLS, TLSImplicit:
	case TLSNone:
		if !isLoopbackHost(c.Host) {
			return fmt.Errorf("%w: tls \"none\" is allowed only for an SMTP relay on localhost", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: tls must be starttls, tls or none", ErrInvalid)
	}
	if strings.ContainsAny(c.Username, "\r\n") {
		return fmt.Errorf("%w: invalid username", ErrInvalid)
	}
	return nil
}

func parseAddress(s string) (*mail.Address, error) {
	a, err := mail.ParseAddress(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("invalid email address %q", s)
	}
	return a, nil
}

// emailSubject is "[DBR²] <severity>: <message>", truncated.
func emailSubject(m Message) string {
	s := strings.Join(strings.Fields(m.Summary()), " ")
	const max = 120
	if r := []rune(s); len(r) > max {
		s = string(r[:max-1]) + "…"
	}
	return "[DBR²] " + m.Severity + ": " + s
}

// emailBody is the plain-text body.
func emailBody(m Message, platformURL string) string {
	var b strings.Builder
	b.WriteString("DBR² notification\n\n")
	fmt.Fprintf(&b, "Severity: %s\n", m.Severity)
	fmt.Fprintf(&b, "Event:    %s\n", m.Type)
	fmt.Fprintf(&b, "Message:  %s\n", m.Summary())
	if m.TargetType != "" || m.TargetID != "" {
		fmt.Fprintf(&b, "Target:   %s %s\n", m.TargetType, m.TargetID)
	}
	fmt.Fprintf(&b, "Time:     %s\n", m.CreatedAt.UTC().Format(time.RFC3339))
	if len(m.Details) > 0 && string(m.Details) != "{}" && string(m.Details) != "null" {
		var buf bytes.Buffer
		if json.Indent(&buf, m.Details, "  ", "  ") == nil {
			b.WriteString("\nDetails:\n  ")
			b.Write(buf.Bytes())
			b.WriteString("\n")
		}
	}
	if platformURL != "" {
		fmt.Fprintf(&b, "\nOpen the console: %s/notifications\n", strings.TrimRight(platformURL, "/"))
	}
	b.WriteString("\n-- \nSent by DBR². Notification channels are managed under System → Notifications.\n")
	return b.String()
}

// buildEmail renders the RFC 5322 message (quoted-printable UTF-8 text).
func buildEmail(from *mail.Address, to []*mail.Address, m Message, platformURL string, now time.Time) ([]byte, error) {
	var buf bytes.Buffer
	var rcpt []string
	for _, a := range to {
		rcpt = append(rcpt, a.String())
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	host := "dbr2"
	if i := strings.LastIndexByte(from.Address, '@'); i >= 0 {
		host = from.Address[i+1:]
	}
	fmt.Fprintf(&buf, "From: %s\r\n", from.String())
	fmt.Fprintf(&buf, "To: %s\r\n", strings.Join(rcpt, ", "))
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", emailSubject(m)))
	fmt.Fprintf(&buf, "Date: %s\r\n", now.Format(time.RFC1123Z))
	fmt.Fprintf(&buf, "Message-ID: <%s@%s>\r\n", hex.EncodeToString(id), host)
	fmt.Fprintf(&buf, "X-DBR2-Event: %s\r\n", headerSafe(m.Type))
	fmt.Fprintf(&buf, "X-DBR2-Delivery: %s\r\n", headerSafe(m.DeliveryID))
	buf.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(emailBody(m, platformURL))); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e {
			return '_'
		}
		return r
	}, s)
}

// sendEmail delivers m to the recipients through the configured relay.
func sendEmail(ctx context.Context, cfg SMTPConfig, rootCAs *x509.CertPool, recipients []string, m Message, platformURL string, now time.Time) (err error) {
	if err := cfg.Validate(); err != nil {
		return err
	}
	from, _ := parseAddress(cfg.From)
	var to []*mail.Address
	for _, r := range recipients {
		a, err := parseAddress(r)
		if err != nil {
			return err
		}
		to = append(to, a)
	}
	if len(to) == 0 {
		return errors.New("no recipients")
	}
	msg, err := buildEmail(from, to, m, platformURL, now)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, SMTPTimeout)
	defer cancel()
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12, RootCAs: rootCAs}
	nd := &net.Dialer{Timeout: SMTPDialTimeout}
	var conn net.Conn
	if cfg.TLS == TLSImplicit {
		conn, err = (&tls.Dialer{NetDialer: nd, Config: tlsCfg}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = nd.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp connect %s: %w", addr, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	defer c.Close()
	if err := c.Hello("dbr2"); err != nil {
		return fmt.Errorf("smtp EHLO: %w", err)
	}
	if cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("smtp: server does not offer STARTTLS")
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("smtp STARTTLS: %w", err)
		}
	}
	if cfg.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server does not offer AUTH")
		}
		if err := c.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("smtp AUTH: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp MAIL FROM: %w", err)
	}
	for _, a := range to {
		if err := c.Rcpt(a.Address); err != nil {
			return fmt.Errorf("smtp RCPT TO %s: %w", a.Address, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp DATA: %w", err)
	}
	_ = c.Quit()
	return nil
}
