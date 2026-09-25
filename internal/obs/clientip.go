// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the caller's IP. X-Forwarded-For is honoured only when the
// direct peer is a trusted proxy (e.g. the dbr2-web container); the rightmost
// untrusted address in the chain is the client. This keeps audit source IPs
// and rate limiting from being spoofed via headers.
func ClientIP(r *http.Request, trusted []netip.Prefix) netip.Addr {
	peer := parseAddr(r.RemoteAddr)
	if !isTrusted(peer, trusted) {
		return peer
	}
	xff := r.Header.Values("X-Forwarded-For")
	var hops []string
	for _, v := range xff {
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a := parseAddr(hops[i])
		if !a.IsValid() {
			break
		}
		if !isTrusted(a, trusted) {
			return a
		}
		peer = a
	}
	return peer
}

func parseAddr(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	if !a.IsValid() {
		return false
	}
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
