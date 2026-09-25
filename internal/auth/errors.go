// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"

	"github.com/jackc/pgx/v5"
)

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
