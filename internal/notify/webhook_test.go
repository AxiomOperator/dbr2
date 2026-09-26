// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSign(t *testing.T) {
	secret, ts, body := []byte("s3cret-s3cret-s3cret"), "1700000000", []byte(`{"id":1}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts + "." + string(body)))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := Sign(secret, ts, body); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
	// Known vector: HMAC-SHA256("key", "1.{}") (openssl dgst -sha256 -hmac key).
	if got := Sign([]byte("key"), "1", []byte("{}")); got != "sha256=1ba6b8171186efc613e8bcc0cbdab2748f24984d7c5a84faa2637afa0e40d224" {
		t.Fatalf("vector mismatch: %s", got)
	}
}

func TestValidateWebhookURL(t *testing.T) {
	for _, u := range []string{"https://hooks.example.com/a?b=c", "http://localhost:9000/x", "http://127.0.0.1/x", "http://[::1]:8080/"} {
		if err := ValidateWebhookURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"", "hooks.example.com", "http://hooks.example.com/", "http://10.0.0.5/", "ftp://example.com/",
		"https://user:pw@example.com/", "https:///path"} {
		if err := ValidateWebhookURL(u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", u)
		}
	}
}

func testMsg() Message {
	return Message{ID: 42, DeliveryID: "7", Type: "backup.failed", Severity: "critical", Message: "Backup of app failed",
		TargetType: "application", TargetID: "abc", Details: json.RawMessage(`{"error":"boom"}`), CreatedAt: time.Unix(1700000000, 0)}
}

func TestWebhookSuccessSigned(t *testing.T) {
	secret := []byte("0123456789abcdef-secret")
	var got struct {
		hdr  http.Header
		body []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.hdr = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	now := time.Unix(1700000100, 0)
	if err := sendWebhook(context.Background(), newWebhookClient(), srv.URL+"/hook", secret, testMsg(), "https://dbr2.example.com", now); err != nil {
		t.Fatal(err)
	}
	if got.hdr.Get("X-DBR2-Event") != "backup.failed" || got.hdr.Get("X-DBR2-Delivery") != "7" || got.hdr.Get("X-DBR2-Timestamp") != "1700000100" {
		t.Fatalf("headers: %v", got.hdr)
	}
	if got.hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("content type %q", got.hdr.Get("Content-Type"))
	}
	if sig := got.hdr.Get("X-DBR2-Signature"); sig != Sign(secret, "1700000100", got.body) {
		t.Fatalf("signature %q does not verify", sig)
	}
	var p WebhookPayload
	if err := json.Unmarshal(got.body, &p); err != nil {
		t.Fatal(err)
	}
	if p.ID != 42 || p.Type != "backup.failed" || p.Severity != "critical" || p.TargetType != "application" || p.TargetID != "abc" ||
		p.PlatformURL != "https://dbr2.example.com" || string(p.Details) != `{"error":"boom"}` || !p.CreatedAt.Equal(time.Unix(1700000000, 0)) {
		t.Fatalf("payload: %+v", p)
	}
}

func TestWebhookUnsignedWithoutSecret(t *testing.T) {
	var sig atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig.Store(r.Header.Get("X-DBR2-Signature"))
	}))
	defer srv.Close()
	if err := sendWebhook(context.Background(), newWebhookClient(), srv.URL, nil, testMsg(), "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if s := sig.Load().(string); s != "" {
		t.Fatalf("signature sent without a secret: %q", s)
	}
}

func TestWebhookServerErrorIsFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "database on fire", http.StatusInternalServerError)
	}))
	defer srv.Close()
	err := sendWebhook(context.Background(), newWebhookClient(), srv.URL, nil, testMsg(), "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") || !strings.Contains(err.Error(), "database on fire") {
		t.Fatalf("err = %v", err)
	}
}

func TestWebhookRedirectRefused(t *testing.T) {
	var followed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Add(1) }))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	err := sendWebhook(context.Background(), newWebhookClient(), srv.URL, nil, testMsg(), "", time.Now())
	if err == nil || !strings.Contains(err.Error(), "redirects are not followed") {
		t.Fatalf("err = %v", err)
	}
	if followed.Load() != 0 {
		t.Fatal("redirect was followed")
	}
}

func TestWebhookTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-block }))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := sendWebhook(ctx, newWebhookClient(), srv.URL, nil, testMsg(), "", time.Now()); err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestWebhookRejectsPlainHTTPRemote(t *testing.T) {
	err := sendWebhook(context.Background(), newWebhookClient(), "http://hooks.example.com/x", nil, testMsg(), "", time.Now())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
