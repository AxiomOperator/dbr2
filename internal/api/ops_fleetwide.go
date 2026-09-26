// SPDX-License-Identifier: Apache-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/AxiomOperator/dbr2/internal/fleet"
	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/protection"
	"github.com/AxiomOperator/dbr2/internal/rbac"
)

// FleetContainerDTO is a container on any host.
type FleetContainerDTO struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Image           string           `json:"image"`
	State           string           `json:"state"`
	HostID          string           `json:"host_id" format:"uuid"`
	Hostname        string           `json:"hostname"`
	ApplicationID   *string          `json:"application_id"`
	ApplicationName *string          `json:"application_name"`
	Ports           []inventory.Port `json:"ports"`
	Mounts          int              `json:"mounts"`
	Networks        []string         `json:"networks"`
	Created         time.Time        `json:"created"`
	RestartPolicy   string           `json:"restart_policy,omitempty"`
}

// FleetVolumeDTO is a named volume on any host.
type FleetVolumeDTO struct {
	Name            string     `json:"name"`
	Driver          string     `json:"driver"`
	HostID          string     `json:"host_id" format:"uuid"`
	Hostname        string     `json:"hostname"`
	Class           string     `json:"class" doc:"local, external or ephemeral (from discovery); unused when no container mounts it."`
	ApplicationID   *string    `json:"application_id"`
	ApplicationName *string    `json:"application_name"`
	UsedBy          []string   `json:"used_by"`
	Protected       bool       `json:"protected" doc:"Contained in the application's latest recovery point."`
	LastBackupAt    *time.Time `json:"last_backup_at"`
	LastSizeBytes   int64      `json:"last_size_bytes"`
}

type fleetInv struct {
	agent fleet.Agent
	inv   *inventory.Inventory
}

func (d *Deps) inventories(ctx context.Context, host string) ([]fleetInv, error) {
	agents, err := d.Fleet.ListAgents(ctx)
	if err != nil {
		return nil, err
	}
	var out []fleetInv
	for _, a := range agents {
		if host != "" && a.ID.String() != host {
			continue
		}
		inv, _, err := d.Fleet.Inventory(ctx, a.ID)
		if errors.Is(err, fleet.ErrNoInventory) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, fleetInv{agent: a, inv: inv})
	}
	return out, nil
}

func registerFleetWide(a huma.API, d *Deps) {
	huma.Register(a, op("list-jobs", http.MethodGet, "/api/v1/jobs", "Backups",
		"List jobs", "Backups and restores in one list, newest first: running, succeeded, partial, failed, rolled back and missing.", rbac.BackupRead),
		func(ctx context.Context, in *struct {
			ApplicationID string `query:"application_id" format:"uuid"`
			Type          string `query:"type" enum:"backup,restore"`
			State         string `query:"state" enum:"running,succeeded,partial,failed,rolled_back,missing"`
			Limit         int32  `query:"limit" minimum:"1" maximum:"500" default:"100"`
		}) (*struct {
			Body struct {
				Items []protection.Job `json:"items"`
			}
		}, error) {
			f := protection.JobFilter{Type: in.Type, State: in.State, Limit: in.Limit}
			if in.ApplicationID != "" {
				id, err := parseID(in.ApplicationID)
				if err != nil {
					return nil, err
				}
				f.ApplicationID = &id
			}
			if in.Type == "restore" && !principal(ctx).Can(rbac.RestoreRead) {
				return nil, problem(http.StatusForbidden, CodeForbidden, "listing restores requires restore.read")
			}
			if in.Type == "" && !principal(ctx).Can(rbac.RestoreRead) {
				f.Type = "backup"
			}
			jobs, err := d.Protection.ListJobs(ctx, f)
			if err != nil {
				return nil, d.protErr(ctx, err)
			}
			out := &struct {
				Body struct {
					Items []protection.Job `json:"items"`
				}
			}{}
			out.Body.Items = append([]protection.Job{}, jobs...)
			return out, nil
		})

	huma.Register(a, op("list-containers", http.MethodGet, "/api/v1/containers", "Hosts",
		"List containers", "Every container on every host from the latest inventories, with the application it belongs to. Secrets are not included.", rbac.HostRead),
		func(ctx context.Context, in *struct {
			HostID string `query:"host_id" format:"uuid"`
		}) (*struct {
			Body struct {
				Items []FleetContainerDTO `json:"items"`
			}
		}, error) {
			invs, err := d.inventories(ctx, in.HostID)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			apps, err := d.Fleet.ListApplications(ctx)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			owner := map[string]fleet.Application{} // agent/container name → app
			for _, ap := range apps {
				if ap.Analysis == nil {
					continue
				}
				for _, c := range ap.Analysis.Containers {
					owner[ap.Record.AgentID.String()+"/"+strings.TrimPrefix(c, "/")] = ap
				}
			}
			out := &struct {
				Body struct {
					Items []FleetContainerDTO `json:"items"`
				}
			}{}
			out.Body.Items = []FleetContainerDTO{}
			for _, fi := range invs {
				for _, c := range fi.inv.Containers {
					dto := FleetContainerDTO{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), Image: c.Image, State: c.State,
						HostID: fi.agent.ID.String(), Hostname: fi.agent.Hostname, Ports: nonNilPorts(c.Ports), Mounts: len(c.Mounts),
						Networks: nonNilStr(c.Networks), Created: c.Created, RestartPolicy: c.RestartPolicy}
					if ap, ok := owner[fi.agent.ID.String()+"/"+dto.Name]; ok {
						id, name := ap.Record.ID.String(), ap.Record.Name
						dto.ApplicationID, dto.ApplicationName = &id, &name
					}
					out.Body.Items = append(out.Body.Items, dto)
				}
			}
			sort.Slice(out.Body.Items, func(i, k int) bool {
				a, b := out.Body.Items[i], out.Body.Items[k]
				return a.Hostname < b.Hostname || (a.Hostname == b.Hostname && a.Name < b.Name)
			})
			return out, nil
		})

	huma.Register(a, op("list-volumes", http.MethodGet, "/api/v1/volumes", "Hosts",
		"List volumes", "Every named volume on every host with its class, the application using it and whether the application's latest recovery point contains it.", rbac.HostRead),
		func(ctx context.Context, in *struct {
			HostID string `query:"host_id" format:"uuid"`
		}) (*struct {
			Body struct {
				Items []FleetVolumeDTO `json:"items"`
			}
		}, error) {
			invs, err := d.inventories(ctx, in.HostID)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			apps, err := d.Fleet.ListApplications(ctx)
			if err != nil {
				return nil, d.fleetErr(ctx, err)
			}
			prot := d.protectionFor(ctx, apps)
			type use struct {
				app fleet.Application
				v   inventory.VolumeUse
			}
			users := map[string]use{}
			for _, ap := range apps {
				if ap.Analysis == nil {
					continue
				}
				for _, v := range ap.Analysis.Volumes {
					users[ap.Record.AgentID.String()+"/"+v.Name] = use{app: ap, v: v}
				}
			}
			out := &struct {
				Body struct {
					Items []FleetVolumeDTO `json:"items"`
				}
			}{}
			out.Body.Items = []FleetVolumeDTO{}
			for _, fi := range invs {
				for _, v := range fi.inv.Volumes {
					dto := FleetVolumeDTO{Name: v.Name, Driver: v.Driver, HostID: fi.agent.ID.String(), Hostname: fi.agent.Hostname,
						Class: "unused", UsedBy: []string{}}
					if u, ok := users[fi.agent.ID.String()+"/"+v.Name]; ok {
						id, name := u.app.Record.ID.String(), u.app.Record.Name
						dto.ApplicationID, dto.ApplicationName, dto.Class, dto.UsedBy = &id, &name, u.v.Class, nonNilStr(u.v.UsedBy)
						if p, ok := prot[u.app.Record.ID]; ok {
							dto.LastBackupAt = p.LastBackupAt
							for _, c := range p.Components {
								if c.Name == "volume:"+v.Name && c.Protected {
									dto.Protected, dto.LastSizeBytes = true, c.LastSizeBytes
								}
							}
						}
					}
					out.Body.Items = append(out.Body.Items, dto)
				}
			}
			sort.Slice(out.Body.Items, func(i, k int) bool {
				a, b := out.Body.Items[i], out.Body.Items[k]
				return a.Hostname < b.Hostname || (a.Hostname == b.Hostname && a.Name < b.Name)
			})
			return out, nil
		})
}
