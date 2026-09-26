// SPDX-License-Identifier: Apache-2.0

package repoclient

import (
	"context"
	"net/http"
)

// RepositoryPassword returns the repository password (re-escrow only; the
// caller must not store it).
func (c *Client) RepositoryPassword(ctx context.Context) (string, error) {
	var out struct {
		Password string `json:"password"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/repository-password", nil, &out)
	return out.Password, err
}
