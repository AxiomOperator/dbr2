// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// WebhookTimeout bounds one webhook request.
const WebhookTimeout = 10 * time.Second

// Message is one notification as sent to a channel.
type Message struct {
	// ID is the outbox notification ID (0 for a test message).
	ID int64
	// DeliveryID identifies this delivery (X-DBR2-Delivery).
	DeliveryID string
	Type       string
	Severity   string
	Message    string
	TargetType string
	TargetID   string
	Details    json.RawMessage
	CreatedAt  time.Time
}

// Summary is the message text, falling back to the event type.
func (m Message) Summary() string {
	if s := strings.TrimSpace(m.Message); s != "" {
		return s
	}
	return m.Type
}

// WebhookPayload is the JSON body POSTed to webhook channels.
type WebhookPayload struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Severity    string          `json:"severity"`
	Message     string          `json:"message"`
	TargetType  string          `json:"target_type,omitempty"`
	TargetID    string          `json:"target_id,omitempty"`
	Details     json.RawMessage `json:"details"`
	CreatedAt   time.Time       `json:"created_at"`
	PlatformURL string          `json:"platform_url,omitempty"`
}

// Sign returns the X-DBR2-Signature value: "sha256=" + hex HMAC-SHA256 of
// timestamp + "." + body keyed with the channel secret.
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// newWebhookClient never follows redirects: a 3xx is a failed delivery (a
// redirect could send the signed payload somewhere the operator did not
// configure).
func newWebhookClient() *http.Client {
	return &http.Client{
		Timeout:       WebhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// ValidateWebhookURL requires an absolute https URL. Plain http is allowed
// only for loopback hosts (localhost, 127.0.0.0/8, ::1), e.g. a relay on the
// same machine. Credentials in the URL are rejected (use the HMAC secret).
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%w: webhook url must be an absolute URL", ErrInvalid)
	}
	if u.User != nil {
		return fmt.Errorf("%w: webhook url must not contain credentials", ErrInvalid)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("%w: webhook url must use https (http is allowed only for localhost)", ErrInvalid)
	}
	return fmt.Errorf("%w: webhook url must use https", ErrInvalid)
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// sendWebhook POSTs m; any 2xx is success.
func sendWebhook(ctx context.Context, client *http.Client, target string, secret []byte, m Message, platformURL string, now time.Time) error {
	if err := ValidateWebhookURL(target); err != nil {
		return err
	}
	details := m.Details
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	body, err := json.Marshal(WebhookPayload{ID: m.ID, Type: m.Type, Severity: m.Severity, Message: m.Message, TargetType: m.TargetType,
		TargetID: m.TargetID, Details: details, CreatedAt: m.CreatedAt.UTC(), PlatformURL: platformURL})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, WebhookTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSpace(target), bytes.NewReader(body))
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DBR2-Notifier/1")
	req.Header.Set("X-DBR2-Event", m.Type)
	req.Header.Set("X-DBR2-Delivery", m.DeliveryID)
	req.Header.Set("X-DBR2-Timestamp", ts)
	if len(secret) > 0 {
		req.Header.Set("X-DBR2-Signature", Sign(secret, ts, body))
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request failed: %w", scrubURLError(err))
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return fmt.Errorf("webhook returned HTTP %d (redirects are not followed)", resp.StatusCode)
	}
	msg := strings.TrimSpace(string(snippet))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	if msg != "" {
		return fmt.Errorf("webhook returned HTTP %d: %s", resp.StatusCode, msg)
	}
	return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
}

// scrubURLError drops the URL from *url.Error (it may carry a token in the
// query string) and keeps the cause.
func scrubURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}
