// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// APIToken describes a personal API token (the secret is never returned again).
type APIToken struct {
	ID         string     `json:"id" format:"uuid"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix" doc:"First characters of the token, for identification."`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type tokenListOutput struct {
	Body struct {
		Items []APIToken `json:"items"`
	}
}

type tokenCreateInput struct {
	Body struct {
		Name          string `json:"name" minLength:"1" maxLength:"100"`
		ExpiresInDays int    `json:"expires_in_days,omitempty" minimum:"0" maximum:"365" default:"90" doc:"0 means no expiry."`
	}
}

type tokenCreateOutput struct {
	Body struct {
		Token    string   `json:"token" doc:"The token secret. Shown only once."`
		APIToken APIToken `json:"api_token"`
	}
}

type tokenIDInput struct {
	ID string `path:"id" format:"uuid"`
}

func registerTokens(a huma.API, d *Deps) {
	huma.Register(a, op("list-api-tokens", http.MethodGet, "/api/v1/tokens", "Authentication",
		"List my API tokens", "Lists the caller's personal API tokens (secrets are never returned).", ""),
		func(ctx context.Context, _ *struct{}) (*tokenListOutput, error) {
			rows, err := d.Auth.ListAPITokens(ctx, principal(ctx))
			if err != nil {
				return nil, d.fail(ctx, err)
			}
			out := &tokenListOutput{}
			out.Body.Items = make([]APIToken, 0, len(rows))
			for _, r := range rows {
				out.Body.Items = append(out.Body.Items, APIToken{ID: r.ID.String(), Name: r.Name, Prefix: r.Prefix,
					CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, LastUsedAt: r.LastUsedAt, RevokedAt: r.RevokedAt})
			}
			return out, nil
		})

	huma.Register(a, withStatus(op("create-api-token", http.MethodPost, "/api/v1/tokens", "Authentication",
		"Create an API token",
		"Creates a personal API token for the CLI, automation or Swagger UI's Authorize dialog. The token carries "+
			"the caller's own permissions, evaluated at every use.", ""), http.StatusCreated),
		func(ctx context.Context, in *tokenCreateInput) (*tokenCreateOutput, error) {
			t, err := d.Auth.CreateAPIToken(ctx, principal(ctx), in.Body.Name, time.Duration(in.Body.ExpiresInDays)*24*time.Hour, metaFrom(ctx))
			if err != nil {
				return nil, d.fail(ctx, err)
			}
			out := &tokenCreateOutput{}
			out.Body.Token = t.Token
			out.Body.APIToken = APIToken{ID: t.ID.String(), Name: t.Name, Prefix: t.Prefix, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
			return out, nil
		})

	huma.Register(a, withStatus(op("revoke-api-token", http.MethodDelete, "/api/v1/tokens/{id}", "Authentication",
		"Revoke an API token", "Revokes one of the caller's API tokens.", "", http.StatusNotFound), http.StatusNoContent),
		func(ctx context.Context, in *tokenIDInput) (*struct{}, error) {
			id, err := uuid.Parse(in.ID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("invalid id")
			}
			return nil, d.fail(ctx, d.Auth.RevokeAPIToken(ctx, principal(ctx), id, metaFrom(ctx)))
		})
}
