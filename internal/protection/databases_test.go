// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/store"
)

func TestDatabaseEngine(t *testing.T) {
	for img, want := range map[string]string{
		"postgres:18": EnginePostgres, "docker.io/library/postgres@sha256:abc": EnginePostgres, "postgis/postgis:16-3.4": EnginePostgres,
		"timescale/timescaledb:latest-pg16": EnginePostgres, "bitnami/postgresql:16": EnginePostgres, "redis:8-alpine": EngineRedis,
		"valkey/valkey:8": EngineRedis, "redis/redis-stack-server": EngineRedis, "registry:5000/team/redis:7": EngineRedis,
		"nginx:1": "", "myorg/postgres-exporter:1": "", "oliver006/redis_exporter": "", "dpage/pgadmin4": "", "bitnami/pgbouncer": "", "rediscommander/redis-commander": "", "busybox": "",
	} {
		if got := databaseEngine(img); got != want {
			t.Errorf("%s: %q, want %q", img, got, want)
		}
	}
}

func dbApp() fleet.Application {
	return fleet.Application{
		Record: store.ListApplicationsRow{ID: uuid.New(), AgentID: uuid.New(), Name: "shop"},
		Analysis: &inventory.Application{Name: "shop",
			Services: []inventory.Service{
				{Name: "db", Image: "postgres:18", Containers: []inventory.ContainerRef{{ID: "c-db", Name: "shop-db-1"}}},
				{Name: "cache", Image: "redis:8", Containers: []inventory.ContainerRef{{ID: "c-cache", Name: "shop-cache-1"}}},
				{Name: "web", Image: "nginx", Containers: []inventory.ContainerRef{{ID: "c-web", Name: "shop-web-1"}}},
			},
			Volumes: []inventory.VolumeUse{{Name: "pgdata", Mountpoint: "/v/pgdata", Protected: true},
				{Name: "shared", Mountpoint: "/v/shared", Protected: true}},
		},
		Inventory: &inventory.Inventory{Containers: []inventory.Container{
			{ID: "c-db", Name: "/shop-db-1", Image: "postgres:18", Env: []inventory.EnvVar{{Key: "POSTGRES_USER", Value: "shop"}},
				Mounts: []inventory.Mount{{Type: "volume", Name: "pgdata"}, {Type: "volume", Name: "shared"}}},
			{ID: "c-cache", Name: "/shop-cache-1", Image: "redis:8"},
			{ID: "c-web", Name: "/shop-web-1", Image: "nginx", Mounts: []inventory.Mount{{Type: "volume", Name: "shared"}}},
		}},
	}
}

func names(p *plan) string {
	var n []string
	for _, c := range p.components {
		n = append(n, c.Name)
	}
	return strings.Join(n, ",")
}

func TestDatabaseStrategies(t *testing.T) {
	agent := fleet.Agent{}
	both, err := buildPlan(dbApp(), agent, BackupSettings{DatabaseStrategy: StrategyBoth})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(both); got != "config,database:db,database:cache,volume:pgdata,volume:shared" {
		t.Fatalf("both: %s", got)
	}
	for _, c := range both.components {
		if c.Name == "database:db" && (c.Database.User != "shop" || c.Database.Format != FormatPGDump || c.Kind != agentv1.ComponentKind_COMPONENT_KIND_DATABASE) {
			t.Fatalf("db spec %+v", c.Database)
		}
		if c.Name == "database:cache" && (!c.Database.IncludeAof || c.Database.Format != FormatRDB) {
			t.Fatalf("cache spec %+v", c.Database)
		}
	}
	logical, _ := buildPlan(dbApp(), agent, BackupSettings{DatabaseStrategy: StrategyLogical})
	if got := names(logical); got != "config,database:db,database:cache,volume:shared" { // pgdata only used by the database
		t.Fatalf("logical: %s", got)
	}
	vol, _ := buildPlan(dbApp(), agent, BackupSettings{DatabaseStrategy: StrategyVolume})
	if got := names(vol); got != "config,volume:pgdata,volume:shared" {
		t.Fatalf("volume: %s", got)
	}
}
