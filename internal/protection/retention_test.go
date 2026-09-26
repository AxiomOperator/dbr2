// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"fmt"
	"testing"
	"time"

	"github.com/AxiomOperator/dbr2/internal/store"
)

// hourly recovery points for 40 days, newest first.
func hourly(now time.Time, n int) []store.ListCommittedForRetentionRow {
	var out []store.ListCommittedForRetentionRow
	for i := 0; i < n; i++ {
		out = append(out, store.ListCommittedForRetentionRow{ID: fmt.Sprintf("rp%04d", i), CreatedAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	return out
}

func TestRetentionGFS(t *testing.T) {
	now := time.Date(2026, 9, 25, 23, 30, 0, 0, time.UTC)
	rps := hourly(now, 40*24)
	cases := []struct {
		name string
		r    Retention
		want int
	}{
		{"last only", Retention{KeepLast: 5}, 5},
		{"daily 7", Retention{KeepLast: 1, KeepDaily: 7}, 7}, // newest per day for 7 days (today's newest = keep_last)
		{"hourly 24 + daily 7", Retention{KeepLast: 1, KeepHourly: 24, KeepDaily: 7}, 24 + 6},
		{"weekly 4", Retention{KeepLast: 1, KeepWeekly: 4}, 4},
		{"monthly 12 over 40 days", Retention{KeepLast: 1, KeepMonthly: 12}, 2},
	}
	for _, c := range cases {
		keep := retentionKeep(rps, c.r, time.UTC)
		if len(keep) != c.want {
			t.Errorf("%s: kept %d, want %d", c.name, len(keep), c.want)
		}
		if !keep[rps[0].ID] {
			t.Errorf("%s: latest not kept", c.name)
		}
	}
}

func TestRetentionNeverDropsLatest(t *testing.T) {
	rps := hourly(time.Now(), 3)
	if keep := retentionKeep(rps, Retention{KeepLast: 0}, time.UTC); !keep[rps[0].ID] || len(keep) != 1 {
		t.Fatalf("keep = %v", keep)
	}
}

func TestContractEvaluation(t *testing.T) {
	now := time.Now()
	rpo := int32(60)
	c := store.RecoveryContract{MaxRpoMinutes: &rpo, RequiredComponents: []string{"volume:data"}}
	if st, _ := evaluateContract(c, nil, now); st != ContractViolated {
		t.Fatal("no recovery point must violate")
	}
	rp := &store.RecoveryPoint{CreatedAt: now.Add(-30 * time.Minute), Manifest: []byte(`{"components":[{"name":"volume:data","status":"succeeded"}]}`)}
	if st, r := evaluateContract(c, rp, now); st != ContractSatisfied {
		t.Fatalf("fresh rp: %s %v", st, r)
	}
	rp.CreatedAt = now.Add(-2 * time.Hour)
	if st, r := evaluateContract(c, rp, now); st != ContractViolated || len(r) != 1 {
		t.Fatalf("stale rp: %s %v", st, r)
	}
	rp.CreatedAt = now
	rp.Manifest = []byte(`{"components":[{"name":"volume:data","status":"failed"}]}`)
	if st, _ := evaluateContract(c, rp, now); st != ContractViolated {
		t.Fatal("missing required component must violate")
	}
}
