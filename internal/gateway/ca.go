// SPDX-License-Identifier: Apache-2.0

// Package gateway is the Agent Gateway hosted in dbr2-server (ADR-0001): the
// mTLS gRPC endpoint agents connect to (enrollment, renewal and the Connect
// session), the command dispatcher used by Temporal activities, and the
// inventory ingestion of Phase 3.
package gateway

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AxiomOperator/dbr2/internal/pki"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// AgentCAName is the pki_authorities row of the agent CA.
const AgentCAName = "agent-ca"

// Box seals secrets at rest (auth.SecretBox).
type Box interface {
	Seal(plaintext []byte, ad string) ([]byte, error)
	Open(sealed []byte, ad string) ([]byte, error)
}

const caKeyAD = "dbr2:pki:agent-ca"

// LoadOrCreateCA loads the agent CA from PostgreSQL, creating it on first
// start. The private key is stored sealed with DBR2_SECRET_KEY (key escrow
// arrives with ADR-0008).
func LoadOrCreateCA(ctx context.Context, q *store.Queries, box Box, orgID uuid.UUID) (*pki.CA, bool, error) {
	row, err := q.GetAuthority(ctx, AgentCAName)
	if errors.Is(err, pgx.ErrNoRows) {
		ca, keyDER, err := pki.NewCA("DBR² Agent CA")
		if err != nil {
			return nil, false, err
		}
		sealed, err := box.Seal(keyDER, caKeyAD)
		if err != nil {
			return nil, false, err
		}
		if err := q.CreateAuthority(ctx, store.CreateAuthorityParams{
			Name: AgentCAName, OrgID: orgID, CertDer: ca.DER, KeyEnc: sealed, Fingerprint: ca.Fingerprint(),
		}); err != nil {
			return nil, false, err
		}
		// Another instance may have won the race: reload what was stored.
		loaded, _, err := LoadOrCreateCA(ctx, q, box, orgID)
		return loaded, loaded != nil && loaded.Fingerprint() == ca.Fingerprint(), err
	}
	if err != nil {
		return nil, false, err
	}
	keyDER, err := box.Open(row.KeyEnc, caKeyAD)
	if err != nil {
		return nil, false, fmt.Errorf("unseal agent CA key (is DBR2_SECRET_KEY the one used at creation?): %w", err)
	}
	ca, err := pki.LoadCA(row.CertDer, keyDER)
	return ca, false, err
}
