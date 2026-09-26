// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/AxiomOperator/dbr2/internal/store"
)

func problemCodes(ps []EscrowProblem) string {
	var c []string
	for _, p := range ps {
		c = append(c, p.Code)
	}
	return strings.Join(c, ",")
}

func TestEscrowProblems(t *testing.T) {
	now := time.Now()
	a, b := store.EscrowRecipient{ID: uuid.New()}, store.EscrowRecipient{ID: uuid.New()}
	confirmed := now.Add(-24 * time.Hour)
	drill := now.Add(-30 * 24 * time.Hour)
	repo := store.Repository{ID: uuid.New(), Name: "primary", Status: "ready", EscrowRecipientIds: []uuid.UUID{b.ID, a.ID}, EscrowConfirmedAt: &confirmed}
	if p := escrowProblems([]store.EscrowRecipient{a, b}, []store.Repository{repo}, &drill, 2, now); len(p) != 0 {
		t.Fatalf("healthy: %v", p)
	}
	if got := problemCodes(escrowProblems([]store.EscrowRecipient{a}, []store.Repository{repo}, &drill, 2, now)); got != "too_few_recipients,recipients_changed" {
		t.Fatalf("removed recipient: %s", got)
	}
	old := now.Add(-100 * 24 * time.Hour)
	repo.EscrowConfirmedAt = &old
	if got := problemCodes(escrowProblems([]store.EscrowRecipient{a, b}, []store.Repository{repo}, nil, 2, now)); got != "reconfirm_due,drill_due" {
		t.Fatalf("stale: %s", got)
	}
	repo.EscrowConfirmedAt, repo.Status = nil, "awaiting_escrow"
	if got := problemCodes(escrowProblems([]store.EscrowRecipient{a, b}, []store.Repository{repo}, &drill, 2, now)); got != "not_confirmed" {
		t.Fatalf("unconfirmed: %s", got)
	}
	repo.Status = "retired"
	if p := escrowProblems([]store.EscrowRecipient{a, b}, []store.Repository{repo}, &drill, 2, now); len(p) != 0 {
		t.Fatalf("retired repositories are ignored: %v", p)
	}
}
