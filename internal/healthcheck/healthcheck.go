// SPDX-License-Identifier: Apache-2.0

// Package healthcheck implements the `healthcheck` subcommand of the DBR²
// binaries. Runtime images are distroless (no shell or curl), so Docker
// HEALTHCHECKs invoke the binary itself.
package healthcheck

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Run GETs url and exits 0 on HTTP 200, 1 otherwise.
func Run(url string) {
	c := &http.Client{Timeout: 4 * time.Second}
	res, err := c.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		os.Exit(1)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy: HTTP", res.StatusCode)
		os.Exit(1)
	}
	os.Exit(0)
}

// LocalURL builds http://127.0.0.1:<port-of-addr><path> from a listen address like ":8080".
func LocalURL(addr, path string) string {
	port := addr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			port = addr[i+1:]
			break
		}
	}
	return "http://127.0.0.1:" + port + path
}
