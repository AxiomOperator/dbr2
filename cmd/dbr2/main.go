// SPDX-License-Identifier: Apache-2.0

// Command dbr2 is the DBR² CLI (component `cli`). It talks to the REST API
// using a personal API token (DBR2_TOKEN, created at /api/v1/tokens).
//
//	dbr2 version [--server URL]   local version, and the server's when given
//	dbr2 whoami  --server URL     identity and permissions of DBR2_TOKEN
package main

import (
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

Environment: DBR2_SERVER (default server URL), DBR2_TOKEN (personal API token).
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
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(server, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", binary+"/"+version.Of(version.CLI))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var p struct{ Detail, Code string }
		_ = json.Unmarshal(body, &p)
		return fmt.Errorf("%s: HTTP %d %s %s", path, res.StatusCode, p.Code, p.Detail)
	}
	return json.Unmarshal(body, out)
}
