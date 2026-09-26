// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MaxAttempts is the number of send attempts before a delivery is failed.
const MaxAttempts = 6

// backoff is the wait after failed attempt n (1-based).
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// Backoff returns the delay before the next attempt after failed attempt n
// (1-based): 1 m, 5 m, 30 m, 2 h, 6 h.
func Backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(backoff) {
		return backoff[len(backoff)-1]
	}
	return backoff[attempt-1]
}

// SeverityRank orders severities (info < warning < critical); unknown = -1.
func SeverityRank(s string) int {
	switch s {
	case SeverityInfo:
		return 0
	case SeverityWarning:
		return 1
	case SeverityCritical:
		return 2
	}
	return -1
}

// MatchEvent reports whether eventType matches a channel's subscription
// list: empty (or "*") matches everything; an entry is an exact event type
// or a prefix wildcard such as "backup.*" (matches backup.failed and
// backup.settings.updated but not "backup" itself).
func MatchEvent(patterns []string, eventType string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		switch {
		case p == "*":
			return true
		case strings.HasSuffix(p, ".*"):
			if strings.HasPrefix(eventType, strings.TrimSuffix(p, "*")) {
				return true
			}
		case p == eventType:
			return true
		}
	}
	return false
}

// Matches reports whether a notification of eventType/severity goes to a
// channel with these filters.
func Matches(patterns []string, minSeverity, eventType, severity string) bool {
	return SeverityRank(severity) >= SeverityRank(minSeverity) && MatchEvent(patterns, eventType)
}

var eventPattern = regexp.MustCompile(`^(\*|[a-z0-9_]+(\.[a-z0-9_]+)*(\.\*)?)$`)

// normalizeEvents validates and de-duplicates a subscription list.
func normalizeEvents(in []string) ([]string, error) {
	if len(in) > 100 {
		return nil, fmt.Errorf("%w: at most 100 event patterns", ErrInvalid)
	}
	out := []string{}
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		if len(e) > 100 || !eventPattern.MatchString(e) {
			return nil, fmt.Errorf("%w: invalid event pattern %q (use an event type such as backup.failed, or a prefix wildcard such as backup.*)", ErrInvalid, e)
		}
		seen[e] = true
		out = append(out, e)
	}
	return out, nil
}
