// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

type preview struct {
	Mode              string   `json:"mode"`
	TargetHostname    string   `json:"target_hostname"`
	Production        bool     `json:"production"`
	ProductionReasons []string `json:"production_reasons"`
	Components        []struct {
		Name, Kind, Action, Target string
		SizeBytes                  int64 `json:"size_bytes"`
	} `json:"components"`
	StopContainers   []struct{ Name, State string }        `json:"stop_containers"`
	CreateContainers []string                              `json:"create_containers"`
	Networks         []struct{ Name, Action string }       `json:"networks"`
	Images           []struct{ Ref, Action string }        `json:"images"`
	Ports            []string                              `json:"ports"`
	Collisions       []struct{ Kind, Name, Detail string } `json:"collisions"`
	Warnings         []string                              `json:"warnings"`
	Blocked          bool                                  `json:"blocked"`
	ApplicationName  string                                `json:"application_name"`
}

func printPreview(p preview) {
	fmt.Printf("Restore %s → %s (%s)\n", p.ApplicationName, p.TargetHostname, strings.ReplaceAll(p.Mode, "_", " "))
	if p.Production {
		fmt.Printf("PRODUCTION RESTORE: %s\n", strings.Join(p.ProductionReasons, "; "))
	}
	for _, c := range p.Components {
		fmt.Printf("  %-10s %-40s → %s (%s)\n", c.Action, c.Name, c.Target, humanBytes(c.SizeBytes))
	}
	for _, c := range p.StopContainers {
		fmt.Printf("  stop       container %s (%s)\n", c.Name, c.State)
	}
	for _, c := range p.CreateContainers {
		fmt.Printf("  create     container %s\n", c)
	}
	for _, n := range p.Networks {
		fmt.Printf("  network    %s: %s\n", n.Name, n.Action)
	}
	for _, i := range p.Images {
		fmt.Printf("  image      %s: %s\n", i.Ref, i.Action)
	}
	if len(p.Ports) > 0 {
		fmt.Printf("  ports      %s\n", strings.Join(p.Ports, ", "))
	}
	for _, w := range p.Warnings {
		fmt.Printf("WARNING: %s\n", w)
	}
	for _, c := range p.Collisions {
		fmt.Printf("COLLISION (%s) %s: %s\n", c.Kind, c.Name, c.Detail)
	}
}

func cmdRestore(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	rp := fs.String("rp", "", "recovery point ID (rp_…)")
	target := fs.String("target-host", "", "target host ID (default: the source host)")
	var comps, remaps multi
	fs.Var(&comps, "component", "component to restore (repeatable; default all)")
	fs.Var(&remaps, "remap", "path remap FROM=TO (repeatable)")
	previewOnly := fs.Bool("preview", false, "show the impact preview and stop")
	reason := fs.String("reason", "", "reason / change ticket (required for production restores)")
	confirm := fs.String("confirm", "", "production restores: the application name, typed")
	wait := fs.Bool("wait", false, "wait for the restore to finish")
	s, err := connect(fs, args)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(*rp, "rp_") {
		return errors.New("--rp is required")
	}
	body := map[string]any{}
	if *target != "" {
		body["target_host_id"] = *target
	}
	if len(comps) > 0 {
		body["components"] = []string(comps)
	}
	var rm []map[string]string
	for _, r := range remaps {
		from, to, ok := strings.Cut(r, "=")
		if !ok {
			return fmt.Errorf("--remap %q: want FROM=TO", r)
		}
		rm = append(rm, map[string]string{"from": from, "to": to})
	}
	if rm != nil {
		body["path_remaps"] = rm
	}
	var p preview
	if err := call(http.MethodPost, s.server, "/api/v1/recovery-points/"+*rp+"/restore-preview", s.token, body, &p); err != nil {
		return err
	}
	printPreview(p)
	if *previewOnly {
		return nil
	}
	if p.Blocked {
		return errors.New("the restore is blocked; resolve the collisions first")
	}
	body["reason"], body["confirmation"] = *reason, *confirm
	var run struct {
		ID string `json:"id"`
	}
	if err := call(http.MethodPost, s.server, "/api/v1/recovery-points/"+*rp+"/restores", s.token, body, &run); err != nil {
		return err
	}
	fmt.Printf("restore %s started\n", run.ID)
	if !*wait {
		return nil
	}
	last := ""
	for deadline := time.Now().Add(24 * time.Hour); time.Now().Before(deadline); {
		time.Sleep(3 * time.Second)
		var r restoreRun
		if err := get(s.server, "/api/v1/restores/"+run.ID, s.token, &r); err != nil {
			return err
		}
		if r.Step != nil && *r.Step != last {
			last = *r.Step
			fmt.Printf("  … %s\n", last)
		}
		switch r.State {
		case "succeeded":
			fmt.Printf("%s succeeded\n", r.ID)
			return nil
		case "failed", "rolled_back":
			return fmt.Errorf("%s %s: %s", r.ID, strings.ReplaceAll(r.State, "_", " "), deref(r.Error))
		}
	}
	return errors.New("timed out waiting (the restore continues on the server)")
}

type restoreRun struct {
	ID              string     `json:"id"`
	RecoveryPointID string     `json:"recovery_point_id"`
	ApplicationName string     `json:"application_name"`
	TargetHostname  string     `json:"target_hostname"`
	Mode            string     `json:"mode"`
	State           string     `json:"state"`
	Step            *string    `json:"step"`
	Error           *string    `json:"error"`
	RequestedBy     string     `json:"requested_by"`
	CreatedAt       time.Time  `json:"created_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

func cmdRestores(args []string) error {
	fs := flag.NewFlagSet("restores", flag.ContinueOnError)
	app := fs.String("app", "", "application ID, name or name@host")
	limit := fs.Int("limit", 50, "maximum rows")
	s, err := connect(fs, args)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("/api/v1/restores?limit=%d", *limit)
	if *app != "" {
		a, err := s.resolveApp(*app)
		if err != nil {
			return err
		}
		q += "&application_id=" + url.QueryEscape(a.ID)
	}
	var list struct{ Items []restoreRun }
	if err := get(s.server, q, s.token, &list); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAPPLICATION\tTARGET\tMODE\tSTATE\tBY\tCREATED")
	for _, r := range list.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.ApplicationName, r.TargetHostname, r.Mode, r.State, r.RequestedBy,
			r.CreatedAt.Local().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}
