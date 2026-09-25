// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

var readyHTTPClient = &http.Client{
	Timeout: 3 * time.Second,
	// Probes target internal services; never follow redirects elsewhere.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// HTTPReadyCheck builds a non-critical readiness check that expects HTTP 200
// from url (e.g. the Caddy edge proxy, dbr2-worker, dbr2-reposerver).
func HTTPReadyCheck(name, url string) ReadyCheck {
	return ReadyCheck{Name: name, Check: func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		res, err := readyHTTPClient.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
		_ = res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: HTTP %d", name, res.StatusCode)
		}
		return nil
	}}
}
