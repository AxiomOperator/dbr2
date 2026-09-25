// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/AxiomOperator/dbr2/internal/store"
)

func storeFor(pool *pgxpool.Pool) *store.Queries { return store.New(pool) }
