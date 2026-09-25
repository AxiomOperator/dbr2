// SPDX-License-Identifier: Apache-2.0

// Package db embeds the PostgreSQL migrations (component `db-schema`).
// Migrations are forward-only in production (ADR-0015); Down sections exist
// for development only.
package db

import "embed"

// Migrations holds the goose migration files.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// DefaultOrgID is the single default organization (multi-tenancy seam).
const DefaultOrgID = "00000000-0000-0000-0000-000000000001"
