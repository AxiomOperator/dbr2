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

type session struct{ server, token string }

func connect(fs *flag.FlagSet, args []string) (*session, error) {
	server := serverFlag(fs)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	s := &session{server: *server, token: os.Getenv("DBR2_TOKEN")}
	if s.server == "" || s.token == "" {
		return nil, errors.New("--server (or DBR2_SERVER) and DBR2_TOKEN are required")
	}
	return s, nil
}

type application struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	DisplayName *string `json:"display_name"`
	Hostname    string  `json:"hostname"`
}

// resolveApp accepts an ID, a name or name@host.
func (s *session) resolveApp(ref string) (application, error) {
	var list struct{ Items []application }
	if err := get(s.server, "/api/v1/applications", s.token, &list); err != nil {
		return application{}, err
	}
	name, host, _ := strings.Cut(ref, "@")
	var hits []application
	for _, a := range list.Items {
		if a.ID == ref {
			return a, nil
		}
		dn := ""
		if a.DisplayName != nil {
			dn = *a.DisplayName
		}
		if (a.Name == name || dn == name) && (host == "" || a.Hostname == host) {
			hits = append(hits, a)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return application{}, fmt.Errorf("no application %q", ref)
	}
	var names []string
	for _, h := range hits {
		names = append(names, h.Name+"@"+h.Hostname)
	}
	return application{}, fmt.Errorf("%q is ambiguous: %s", ref, strings.Join(names, ", "))
}

type recoveryPoint struct {
	ID              string     `json:"id"`
	ApplicationName string     `json:"application_name"`
	Hostname        string     `json:"hostname"`
	State           string     `json:"state"`
	Status          *string    `json:"status"`
	ConsistencyMode string     `json:"consistency_mode"`
	WorkflowID      string     `json:"workflow_id"`
	SizeBytes       int64      `json:"size_bytes"`
	ComponentCount  int32      `json:"component_count"`
	Error           *string    `json:"error"`
	CreatedAt       time.Time  `json:"created_at"`
	CommittedAt     *time.Time `json:"committed_at"`
}

func cmdBackup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	app := fs.String("app", "", "application ID, name or name@host")
	mode := fs.String("mode", "", "consistency mode override: live, quiesced or offline")
	wait := fs.Bool("wait", false, "wait for the backup to finish")
	timeout := fs.Duration("timeout", 6*time.Hour, "maximum time to wait with --wait")
	s, err := connect(fs, args)
	if err != nil {
		return err
	}
	if *app == "" {
		return errors.New("--app is required")
	}
	a, err := s.resolveApp(*app)
	if err != nil {
		return err
	}
	var body any
	if *mode != "" {
		body = map[string]string{"consistency_mode": *mode}
	}
	started := time.Now().Add(-5 * time.Second)
	var out struct {
		WorkflowID string `json:"workflow_id"`
	}
	if err := call(http.MethodPost, s.server, "/api/v1/applications/"+a.ID+"/backups", s.token, body, &out); err != nil {
		return err
	}
	fmt.Printf("backup of %s@%s started (workflow %s)\n", a.Name, a.Hostname, out.WorkflowID)
	if !*wait {
		return nil
	}
	deadline := time.Now().Add(*timeout)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		var list struct{ Items []recoveryPoint }
		if err := get(s.server, "/api/v1/recovery-points?limit=5&application_id="+url.QueryEscape(a.ID), s.token, &list); err != nil {
			return err
		}
		for _, rp := range list.Items {
			if rp.CreatedAt.Before(started) {
				continue
			}
			switch rp.State {
			case "committed":
				fmt.Printf("%s committed: %s, %s, %d components, %s\n", rp.ID, deref(rp.Status), rp.ConsistencyMode, rp.ComponentCount, humanBytes(rp.SizeBytes))
				return nil
			case "failed":
				return fmt.Errorf("%s failed: %s", rp.ID, deref(rp.Error))
			}
		}
	}
	return errors.New("timed out waiting for the backup (it continues on the server)")
}

func cmdRecoveryPoints(args []string) error {
	fs := flag.NewFlagSet("recovery-points", flag.ContinueOnError)
	app := fs.String("app", "", "application ID, name or name@host")
	limit := fs.Int("limit", 50, "maximum rows")
	s, err := connect(fs, args)
	if err != nil {
		return err
	}
	q := fmt.Sprintf("/api/v1/recovery-points?limit=%d", *limit)
	if *app != "" {
		a, err := s.resolveApp(*app)
		if err != nil {
			return err
		}
		q += "&application_id=" + url.QueryEscape(a.ID)
	}
	var list struct{ Items []recoveryPoint }
	if err := get(s.server, q, s.token, &list); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tAPPLICATION\tHOST\tSTATE\tSTATUS\tMODE\tSIZE\tCREATED")
	for _, rp := range list.Items {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", rp.ID, rp.ApplicationName, rp.Hostname, rp.State, deref(rp.Status),
			rp.ConsistencyMode, humanBytes(rp.SizeBytes), rp.CreatedAt.Local().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}

func cmdAdmin(args []string) error {
	if len(args) == 0 || args[0] != "reindex" {
		return errors.New("usage: dbr2 admin reindex --repository REPO")
	}
	fs := flag.NewFlagSet("admin reindex", flag.ContinueOnError)
	repo := fs.String("repository", "", "Repository ID or name")
	s, err := connect(fs, args[1:])
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
	}
	if err := get(s.server, "/api/v1/repositories", s.token, &list); err != nil {
		return err
	}
	id := ""
	for _, r := range list.Items {
		if r.ID == *repo || r.Name == *repo {
			id = r.ID
		}
	}
	if id == "" {
		return fmt.Errorf("no Repository %q", *repo)
	}
	var out struct {
		WorkflowID string `json:"workflow_id"`
	}
	if err := call(http.MethodPost, s.server, "/api/v1/repositories/"+id+"/reindex", s.token, nil, &out); err != nil {
		return err
	}
	fmt.Printf("reindex started (workflow %s)\n", out.WorkflowID)
	return nil
}

func deref(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
