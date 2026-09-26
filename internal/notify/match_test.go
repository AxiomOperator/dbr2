// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"errors"
	"testing"
	"time"
)

func TestMatchEvent(t *testing.T) {
	cases := []struct {
		patterns []string
		event    string
		want     bool
	}{
		{nil, "backup.failed", true},
		{[]string{}, "anything", true},
		{[]string{"*"}, "agent.offline", true},
		{[]string{"backup.failed"}, "backup.failed", true},
		{[]string{"backup.failed"}, "backup.completed", false},
		{[]string{"backup.*"}, "backup.failed", true},
		{[]string{"backup.*"}, "backup.settings.updated", true},
		{[]string{"backup.*"}, "backup", false},
		{[]string{"backup.*"}, "backups.failed", false},
		{[]string{"restore.failed", "agent.*"}, "agent.online", true},
		{[]string{"restore.failed", "agent.*"}, "restore.succeeded", false},
		{[]string{"quiesce.*"}, "quiesce.auto_resumed", true},
	}
	for _, c := range cases {
		if got := MatchEvent(c.patterns, c.event); got != c.want {
			t.Errorf("MatchEvent(%v, %q) = %v, want %v", c.patterns, c.event, got, c.want)
		}
	}
}

func TestMatchesSeverity(t *testing.T) {
	cases := []struct {
		min, sev string
		want     bool
	}{
		{"info", "info", true},
		{"info", "critical", true},
		{"warning", "info", false},
		{"warning", "warning", true},
		{"warning", "critical", true},
		{"critical", "warning", false},
		{"critical", "critical", true},
		{"warning", "bogus", false},
	}
	for _, c := range cases {
		if got := Matches(nil, c.min, "x.y", c.sev); got != c.want {
			t.Errorf("Matches(min=%s, sev=%s) = %v, want %v", c.min, c.sev, got, c.want)
		}
	}
	if Matches([]string{"backup.*"}, "info", "restore.failed", "critical") {
		t.Error("event filter ignored")
	}
}

func TestBackoffSchedule(t *testing.T) {
	want := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := Backoff(i + 1); got != w {
			t.Errorf("Backoff(%d) = %v, want %v", i+1, got, w)
		}
	}
	if Backoff(0) != time.Minute || Backoff(9) != 6*time.Hour {
		t.Error("out-of-range attempts not clamped")
	}
	if MaxAttempts != 6 {
		t.Errorf("MaxAttempts = %d, want 6", MaxAttempts)
	}
}

func TestNormalizeEvents(t *testing.T) {
	got, err := normalizeEvents([]string{" backup.failed ", "backup.failed", "", "agent.*", "*"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "backup.failed" || got[1] != "agent.*" || got[2] != "*" {
		t.Fatalf("normalized = %v", got)
	}
	for _, bad := range []string{"Backup.Failed", "backup.*.x", "backup*", ".x", "a b", "backup.", "*.failed"} {
		if _, err := normalizeEvents([]string{bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, _ := normalizeEvents(nil); got == nil || len(got) != 0 {
		t.Errorf("nil → %v, want empty slice", got)
	}
}

func TestValidateChannel(t *testing.T) {
	s := func(v string) *string { return &v }
	ok := []struct {
		kind string
		in   ChannelInput
	}{
		{KindEmail, ChannelInput{Name: "ops", To: []string{"Ops <ops@example.com>"}}},
		{KindWebhook, ChannelInput{Name: "hook", URL: "https://hooks.example.com/x", Secret: s("0123456789abcdef")}},
		{KindWebhook, ChannelInput{Name: "local", URL: "http://localhost:8080/x", Secret: s("")}},
	}
	for _, c := range ok {
		v, err := validateChannel(c.kind, c.in)
		if err != nil {
			t.Errorf("%+v: %v", c.in, err)
		}
		if v.minSev != SeverityWarning {
			t.Errorf("default min severity = %q", v.minSev)
		}
	}
	bad := []struct {
		kind string
		in   ChannelInput
	}{
		{KindEmail, ChannelInput{Name: "", To: []string{"a@example.com"}}},
		{KindEmail, ChannelInput{Name: "x"}},
		{KindEmail, ChannelInput{Name: "x", To: []string{"not an address"}}},
		{KindEmail, ChannelInput{Name: "x", To: []string{"a@example.com"}, Secret: s("0123456789abcdef")}},
		{KindWebhook, ChannelInput{Name: "x", URL: "http://hooks.example.com/x"}},
		{KindWebhook, ChannelInput{Name: "x", URL: "https://hooks.example.com/x", Secret: s("short")}},
		{KindWebhook, ChannelInput{Name: "x", URL: "https://hooks.example.com/x", MinSeverity: "debug"}},
		{"sms", ChannelInput{Name: "x"}},
	}
	for _, c := range bad {
		if _, err := validateChannel(c.kind, c.in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %+v accepted (err %v)", c.kind, c.in, err)
		}
	}
}
