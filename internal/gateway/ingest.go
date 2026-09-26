// SPDX-License-Identifier: Apache-2.0

package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AxiomOperator/dbr2/internal/events"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/rbac"
	"github.com/AxiomOperator/dbr2/internal/store"
)

// IngestInventory stores an agent's inventory (sealing secrets first) and
// reconciles its discovered applications. Older inventories than the stored
// one are ignored. It returns the number of applications found.
func (g *Gateway) IngestInventory(ctx context.Context, agentID string, data []byte) (int, error) {
	n, err := g.ingestInventory(ctx, agentID, data)
	if err == nil {
		g.publish(ctx, events.New(events.InventoryUpd, rbac.HostRead, map[string]any{"host_id": agentID, "applications": n}))
	}
	return n, err
}

func (g *Gateway) ingestInventory(ctx context.Context, agentID string, data []byte) (int, error) {
	id, err := uuid.Parse(agentID)
	if err != nil {
		return 0, err
	}
	var inv inventory.Inventory
	if err := json.Unmarshal(data, &inv); err != nil {
		return 0, fmt.Errorf("decode inventory: %w", err)
	}
	if inv.SchemaVersion < 1 || inv.SchemaVersion > inventory.SchemaVersion {
		return 0, fmt.Errorf("unsupported inventory schema version %d", inv.SchemaVersion)
	}
	if err := inventory.Seal(&inv, g.box, inventory.SealAD(agentID)); err != nil {
		return 0, err
	}
	sealed, err := json.Marshal(&inv)
	if err != nil {
		return 0, err
	}
	var count int
	err = pgx.BeginFunc(ctx, g.pool, func(tx pgx.Tx) error {
		q := g.q.WithTx(tx)
		ag, err := q.GetAgent(ctx, id)
		if err != nil {
			return err
		}
		if err := q.UpsertInventorySnapshot(ctx, store.UpsertInventorySnapshotParams{
			AgentID: id, OrgID: ag.OrgID, SchemaVersion: int32(inv.SchemaVersion), CollectedAt: inv.CollectedAt, Data: sealed,
		}); err != nil {
			return err
		}
		count, err = Reconcile(ctx, q, ag, &inv)
		return err
	})
	return count, err
}

// Reconcile updates an agent's applications from an inventory: discovered
// Compose/standalone applications are upserted, vanished ones are marked
// missing, and standalone applications whose containers a manual
// application claims are removed. It returns the number of applications.
func Reconcile(ctx context.Context, q *store.Queries, ag store.Agent, inv *inventory.Inventory) (int, error) {
	existing, err := q.ListAgentApplications(ctx, ag.ID)
	if err != nil {
		return 0, err
	}
	manual := map[string][]string{}
	var claimed []string
	for _, a := range existing {
		if a.Kind == inventory.KindManual {
			manual[a.Key] = a.ManualContainers
			for _, c := range a.ManualContainers {
				claimed = append(claimed, inventory.KindContainer+":"+c)
			}
		}
	}
	if len(claimed) > 0 {
		if err := q.DeleteContainerApplications(ctx, store.DeleteContainerApplicationsParams{AgentID: ag.ID, Keys: claimed}); err != nil {
			return 0, err
		}
	}
	apps := inventory.Analyze(inv, manual)
	var keys []string
	for _, a := range apps {
		keys = append(keys, a.Key)
		if a.Kind == inventory.KindManual {
			continue
		}
		if err := q.UpsertDiscoveredApplication(ctx, store.UpsertDiscoveredApplicationParams{
			OrgID: ag.OrgID, AgentID: ag.ID, Key: a.Key, Kind: a.Kind, Name: a.Name, LastSeenAt: inv.CollectedAt,
		}); err != nil {
			return 0, err
		}
	}
	err = q.MarkApplicationsMissing(ctx, store.MarkApplicationsMissingParams{AgentID: ag.ID, Column2: true, MissingSince: &inv.CollectedAt, PresentKeys: keys})
	return len(apps), err
}
