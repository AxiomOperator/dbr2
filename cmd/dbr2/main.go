// SPDX-License-Identifier: Apache-2.0

// Command dbr2 is the DBR² CLI (component `cli`). It talks to the REST API
// using a personal API token (DBR2_TOKEN, created at /api/v1/tokens).
//
//	dbr2 version [--server URL]   local version, and the server's when given
//	dbr2 whoami  --server URL     identity and permissions of DBR2_TOKEN
//	dbr2 backup  --app APP [--mode M] [--wait]   back up an application now
//	dbr2 recovery-points [--app APP]             list recovery points
//	dbr2 admin reindex --repository REPO          rebuild the recovery-point index
//	dbr2 restore --rp RP [--preview] [--wait]     restore a recovery point
//	dbr2 restores [--app APP]                     restore history
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/AxiomOperator/dbr2/internal/version"
)

const binary = "dbr2"

func main() {
	if err := version.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "version", "--version":
		err = cmdVersion(os.Args[2:])
	case "whoami":
		err = cmdWhoami(os.Args[2:])
	case "backup":
		err = cmdBackup(os.Args[2:])
	case "recovery-points", "rps":
		err = cmdRecoveryPoints(os.Args[2:])
	case "restore":
		err = cmdRestore(os.Args[2:])
	case "restores":
		err = cmdRestores(os.Args[2:])
	case "admin":
		err = cmdAdmin(os.Args[2:])
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binary, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  dbr2 version [--server URL]
  dbr2 whoami --server URL        (token from DBR2_TOKEN)
  dbr2 backup --app APP [--mode live|quiesced|offline] [--wait]
  dbr2 recovery-points [--app APP] [--limit N]
  dbr2 admin reindex --repository REPO
  dbr2 admin restore-platform     (runs on the server host: see dbr2-server admin restore-platform)
  dbr2 restore --rp RP [--target-host ID] [--component NAME]... [--remap FROM=TO]...
               [--preview] [--reason TEXT] [--confirm APP-NAME] [--wait]
  dbr2 restores [--app APP]

APP is an application ID or name ("name@host" when ambiguous); REPO is a
Repository ID or name.

Environment: DBR2_SERVER (default server URL), DBR2_TOKEN (personal API token),
DBR2_CA_FILE (extra PEM CA bundle, e.g. the proxy's internal CA).
`)
}

func serverFlag(fs *flag.FlagSet) *string {
	return fs.String("server", os.Getenv("DBR2_SERVER"), "DBR² server URL, e.g. https://dbr2.example.lan")
}

func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	server := serverFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	fmt.Println(version.String(binary, version.CLI))
	if *server == "" {
		return nil
	}
	var v struct {
		Platform   string            `json:"platform"`
		Components map[string]string `json:"components"`
	}
	if err := get(*server, "/api/v1/version", "", &v); err != nil {
		return err
	}
	fmt.Printf("server platform %s (api %s, server %s)\n", v.Platform, v.Components["api"], v.Components["server"])
	return nil
}

func cmdWhoami(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	server := serverFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	token := os.Getenv("DBR2_TOKEN")
	if *server == "" || token == "" {
		return errors.New("--server (or DBR2_SERVER) and DBR2_TOKEN are required")
	}
	var me struct {
		Username    string   `json:"username"`
		Kind        string   `json:"kind"`
		Roles       []string `json:"roles"`
		Permissions []string `json:"permissions"`
	}
	if err := get(*server, "/api/v1/auth/me", token, &me); err != nil {
		return err
	}
	fmt.Printf("%s (%s)\nroles: %s\npermissions: %s\n", me.Username, me.Kind, strings.Join(me.Roles, ", "), strings.Join(me.Permissions, ", "))
	return nil
}

func get(server, path, token string, out any) error {
	return call(http.MethodGet, server, path, token, nil, out)
}

func httpClient() (*http.Client, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	if f := os.Getenv("DBR2_CA_FILE"); f != "" {
		pem, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("DBR2_CA_FILE %s: no certificates", f)
		}
		c.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}
	return c, nil
}

func call(method, server, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimRight(server, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", binary+"/"+version.Of(version.CLI))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc, err := httpClient()
	if err != nil {
		return err
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode/100 != 2 {
		var p struct{ Detail, Code string }
		_ = json.Unmarshal(data, &p)
		return fmt.Errorf("%s %s: HTTP %d %s %s", method, path, res.StatusCode, p.Code, p.Detail)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
