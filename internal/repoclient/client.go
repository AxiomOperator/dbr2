// SPDX-License-Identifier: Apache-2.0

// Package repoclient is the client for the dbr2-reposerver management API
// (internal network only, Bearer internal token; ADR-0002).
package repoclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Status is GET /v1/status.
type Status struct {
	RepositoryID   string `json:"repository_id"`
	Initialized    bool   `json:"initialized"`
	ServerRunning  bool   `json:"server_running"`
	KopiaAddress   string `json:"kopia_address"`
	CertSHA256     string `json:"cert_sha256"`
	KopiaVersion   string `json:"kopia_version"`
	Splitter       string `json:"splitter"`
	StoragePath    string `json:"storage_path"`
	StorageHealthy bool   `json:"storage_healthy"`
	StorageError   string `json:"storage_error"`
	StorageTotal   uint64 `json:"storage_total_bytes"`
	StorageFree    uint64 `json:"storage_free_bytes"`
	StorageUsed    uint64 `json:"storage_used_bytes"`
}

// Error is a non-2xx response.
type Error struct {
	StatusCode int
	Code       string `json:"code"`
	Message    string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("reposerver: %d %s: %s", e.StatusCode, e.Code, e.Message)
}

// IsCode reports whether err is a reposerver error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

// Client talks to one reposerver.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New returns a client for baseURL (e.g. http://dbr2-reposerver:8091).
func New(baseURL, token string) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		e := &Error{StatusCode: resp.StatusCode}
		if json.Unmarshal(b, e) != nil || (e.Code == "" && e.Message == "") {
			e.Message = strings.TrimSpace(string(b))
		}
		return e
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// Status returns the reposerver status.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	var s Status
	return &s, c.do(ctx, http.MethodGet, "/v1/status", nil, &s)
}

// Initialize creates the Kopia repository (409 already_initialized, 422
// storage_not_ready).
func (c *Client) Initialize(ctx context.Context, password, splitter string) (*Status, error) {
	var s Status
	return &s, c.do(ctx, http.MethodPost, "/v1/initialize", map[string]string{"password": password, "splitter": splitter}, &s)
}

// SetUser creates or updates a Kopia server user (user@host).
func (c *Client) SetUser(ctx context.Context, username, password string) error {
	return c.do(ctx, http.MethodPut, "/v1/users/"+url.PathEscape(username), map[string]string{"password": password}, nil)
}

// DeleteUser removes a Kopia server user.
func (c *Client) DeleteUser(ctx context.Context, username string) error {
	return c.do(ctx, http.MethodDelete, "/v1/users/"+url.PathEscape(username), nil, nil)
}

// GrantRead grants user temporary READ on another source's snapshots
// (cross-host restore); returns the grant ID.
func (c *Client) GrantRead(ctx context.Context, user, sourceUser, sourceHost string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := c.do(ctx, http.MethodPost, "/v1/acl/read-grants", map[string]string{"user": user, "source_user": sourceUser, "source_host": sourceHost}, &out)
	return out.ID, err
}

// RevokeRead removes a read grant.
func (c *Client) RevokeRead(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/acl/read-grants/"+url.PathEscape(id), nil, nil)
}
