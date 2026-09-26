// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal in-process SMTP server compatible with net/smtp:
// EHLO/HELO, STARTTLS, AUTH PLAIN (initial response), MAIL, RCPT, DATA,
// RSET, NOOP, QUIT; optionally implicit TLS.
type fakeSMTP struct {
	ln       net.Listener
	tlsCfg   *tls.Config
	implicit bool
	user     string
	pass     string

	mu   sync.Mutex
	msgs []fakeMail
}

type fakeMail struct {
	from   string
	to     []string
	data   []byte
	tls    bool
	authed string
}

func newFakeSMTP(t *testing.T, tlsCfg *tls.Config, implicit bool, user, pass string) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, tlsCfg: tlsCfg, implicit: implicit, user: user, pass: pass}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) messages() []fakeMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeMail(nil), f.msgs...)
}

func (f *fakeSMTP) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	isTLS := false
	if f.implicit {
		tc := tls.Server(c, f.tlsCfg)
		if err := tc.Handshake(); err != nil {
			return
		}
		c, isTLS = tc, true
	}
	tp := textproto.NewConn(c)
	_ = tp.PrintfLine("220 fake.test ESMTP")
	var cur fakeMail
	authed := ""
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO", "HELO":
			exts := []string{"fake.test"}
			if f.tlsCfg != nil && !isTLS {
				exts = append(exts, "STARTTLS")
			}
			if f.user != "" {
				exts = append(exts, "AUTH PLAIN")
			}
			exts = append(exts, "8BITMIME")
			for i, e := range exts {
				sep := "-"
				if i == len(exts)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, e)
			}
		case "STARTTLS":
			_ = tp.PrintfLine("220 go ahead")
			tc := tls.Server(c, f.tlsCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, isTLS = tc, true
			tp = textproto.NewConn(c)
		case "AUTH":
			mech, resp, _ := strings.Cut(arg, " ")
			raw, err := base64.StdEncoding.DecodeString(resp)
			parts := strings.Split(string(raw), "\x00")
			if strings.ToUpper(mech) != "PLAIN" || err != nil || len(parts) != 3 || parts[1] != f.user || parts[2] != f.pass {
				_ = tp.PrintfLine("535 authentication failed")
				continue
			}
			authed = parts[1]
			_ = tp.PrintfLine("235 ok")
		case "MAIL":
			if f.user != "" && authed == "" {
				_ = tp.PrintfLine("530 authentication required")
				continue
			}
			cur = fakeMail{from: strings.Trim(strings.TrimPrefix(arg, "FROM:"), "<>"), tls: isTLS, authed: authed}
			if i := strings.Index(cur.from, ">"); i >= 0 {
				cur.from = cur.from[:i]
			}
			_ = tp.PrintfLine("250 ok")
		case "RCPT":
			cur.to = append(cur.to, strings.Trim(strings.TrimPrefix(arg, "TO:"), "<>"))
			_ = tp.PrintfLine("250 ok")
		case "DATA":
			_ = tp.PrintfLine("354 go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			cur.data = data
			f.mu.Lock()
			f.msgs = append(f.msgs, cur)
			f.mu.Unlock()
			_ = tp.PrintfLine("250 queued")
		case "RSET", "NOOP":
			_ = tp.PrintfLine("250 ok")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("502 not implemented")
		}
	}
}

// selfSigned returns a server TLS config for 127.0.0.1 and a pool trusting it.
func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fake smtp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, pool
}

// parseMail returns the decoded subject, headers and body.
func parseMail(t *testing.T, data []byte) (string, mail.Header, string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	subj, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatal(err)
	}
	return subj, msg.Header, string(body)
}

func TestEmailPlainLocalhostWithAuth(t *testing.T) {
	f := newFakeSMTP(t, nil, false, "mailer", "hunter22")
	cfg := SMTPConfig{Host: "127.0.0.1", Port: f.port(), Username: "mailer", Password: "hunter22", From: "DBR2 <dbr2@example.com>", TLS: TLSNone}
	m := testMsg()
	if err := sendEmail(context.Background(), cfg, nil, []string{"ops@example.com", "Oncall <oncall@example.com>"}, m, "https://dbr2.example.com/", time.Now()); err != nil {
		t.Fatal(err)
	}
	msgs := f.messages()
	if len(msgs) != 1 {
		t.Fatalf("%d messages", len(msgs))
	}
	got := msgs[0]
	if got.from != "dbr2@example.com" || len(got.to) != 2 || got.to[1] != "oncall@example.com" || got.authed != "mailer" {
		t.Fatalf("envelope: %+v", got)
	}
	subj, hdr, body := parseMail(t, got.data)
	if subj != "[DBR²] critical: Backup of app failed" {
		t.Fatalf("subject %q", subj)
	}
	if hdr.Get("X-DBR2-Event") != "backup.failed" || !strings.Contains(hdr.Get("Content-Type"), "text/plain") {
		t.Fatalf("headers %v", hdr)
	}
	for _, want := range []string{"Severity: critical", "Event:    backup.failed", "Target:   application abc", "https://dbr2.example.com/notifications", `"error": "boom"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}

func TestEmailSTARTTLS(t *testing.T) {
	srvTLS, roots := selfSigned(t)
	f := newFakeSMTP(t, srvTLS, false, "u", "p")
	cfg := SMTPConfig{Host: "127.0.0.1", Port: f.port(), Username: "u", Password: "p", From: "dbr2@example.com", TLS: TLSStartTLS}
	if err := sendEmail(context.Background(), cfg, roots, []string{"ops@example.com"}, testMsg(), "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if msgs := f.messages(); len(msgs) != 1 || !msgs[0].tls {
		t.Fatalf("messages: %+v", msgs)
	}
	// Untrusted certificate: fails.
	if err := sendEmail(context.Background(), cfg, nil, []string{"ops@example.com"}, testMsg(), "", time.Now()); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestEmailSTARTTLSRequired(t *testing.T) {
	f := newFakeSMTP(t, nil, false, "", "") // no STARTTLS offered
	cfg := SMTPConfig{Host: "127.0.0.1", Port: f.port(), From: "dbr2@example.com", TLS: TLSStartTLS}
	err := sendEmail(context.Background(), cfg, nil, []string{"ops@example.com"}, testMsg(), "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("err = %v", err)
	}
	if len(f.messages()) != 0 {
		t.Fatal("sent without TLS")
	}
}

func TestEmailImplicitTLS(t *testing.T) {
	srvTLS, roots := selfSigned(t)
	f := newFakeSMTP(t, srvTLS, true, "", "")
	cfg := SMTPConfig{Host: "127.0.0.1", Port: f.port(), From: "dbr2@example.com", TLS: TLSImplicit}
	if err := sendEmail(context.Background(), cfg, roots, []string{"ops@example.com"}, testMsg(), "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if msgs := f.messages(); len(msgs) != 1 || !msgs[0].tls {
		t.Fatalf("messages: %+v", msgs)
	}
}

func TestEmailWrongPassword(t *testing.T) {
	f := newFakeSMTP(t, nil, false, "u", "right")
	cfg := SMTPConfig{Host: "localhost", Port: f.port(), Username: "u", Password: "wrong", From: "dbr2@example.com", TLS: TLSNone}
	err := sendEmail(context.Background(), cfg, nil, []string{"ops@example.com"}, testMsg(), "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "AUTH") {
		t.Fatalf("err = %v", err)
	}
}

func TestEmailConnectTimeoutHonorsContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accept but never greet
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cfg := SMTPConfig{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, From: "dbr2@example.com", TLS: TLSNone}
	start := time.Now()
	if err := sendEmail(ctx, cfg, nil, []string{"ops@example.com"}, testMsg(), "", time.Now()); err == nil {
		t.Fatal("expected an error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("context deadline not honored")
	}
}

func TestSMTPConfigValidate(t *testing.T) {
	good := SMTPConfig{Host: "smtp.example.com", Port: 587, From: "dbr2@example.com", TLS: TLSStartTLS}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []SMTPConfig{
		{Host: "smtp.example.com", Port: 25, From: "dbr2@example.com", TLS: TLSNone}, // plain text off-host
		{Host: "", Port: 587, From: "dbr2@example.com", TLS: TLSStartTLS},
		{Host: "smtp.example.com", Port: 0, From: "dbr2@example.com", TLS: TLSStartTLS},
		{Host: "smtp.example.com", Port: 587, From: "nope", TLS: TLSStartTLS},
		{Host: "smtp.example.com", Port: 587, From: "dbr2@example.com", TLS: "ssl"},
	}
	for _, c := range bad {
		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v accepted", c)
		}
	}
	for _, h := range []string{"localhost", "127.0.0.1", "::1"} {
		c := SMTPConfig{Host: h, Port: 25, From: "dbr2@example.com", TLS: TLSNone}
		if err := c.Validate(); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
}

func TestEmailSubjectTruncatedAndFallback(t *testing.T) {
	m := testMsg()
	m.Message = strings.Repeat("x", 500)
	s := emailSubject(m)
	if r := []rune(s); len(r) > 150 || !strings.HasSuffix(s, "…") {
		t.Fatalf("subject not truncated: %d runes", len(r))
	}
	m.Message = "  \n "
	if got := emailSubject(m); got != "[DBR²] critical: backup.failed" {
		t.Fatalf("fallback subject %q", got)
	}
	m.Message = "line1\r\nBcc: evil@example.com"
	if got := emailSubject(m); strings.ContainsAny(got, "\r\n") {
		t.Fatalf("header injection: %q", got)
	}
}
