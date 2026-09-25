// SPDX-License-Identifier: Apache-2.0

// Command dbr2-agent is the DBR² data-plane agent (component `agent`,
// ADR-0006): a native systemd service on each protected Docker host. It
// enrolls with a single-use token, keeps an outbound mTLS session to the
// Agent Gateway, runs commands from a durable journal (ADR-0001) and reports
// its Docker inventory (Phase 3).
//
//	dbr2-agent enroll --server <host:port> --token <token> --ca-sha256 <fingerprint>
//	dbr2-agent run [--config /etc/dbr2/agent.yaml]
//	dbr2-agent status [--config /etc/dbr2/agent.yaml]
//	dbr2-agent version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AxiomOperator/dbr2/internal/agent"
	"github.com/AxiomOperator/dbr2/internal/obs"
	"github.com/AxiomOperator/dbr2/internal/runtime"
	"github.com/AxiomOperator/dbr2/internal/version"
)

const binary = "dbr2-agent"

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
	case "version", "--version", "-version":
		fmt.Println(version.String(binary, version.Agent))
		fmt.Printf("agent-protocol %s\n", version.Of(version.AgentProtocol))
		return
	case "enroll":
		err = enroll(os.Args[2:])
	case "run":
		err = run(os.Args[2:])
	case "status":
		err = status(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binary, err)
		if errors.Is(err, agent.ErrNotEnrolled) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `Usage:
  dbr2-agent enroll --server <host:port> --token <token> --ca-sha256 <fingerprint> [--config file] [--state-dir dir] [--force]
  dbr2-agent run    [--config /etc/dbr2/agent.yaml]
  dbr2-agent status [--config /etc/dbr2/agent.yaml]
  dbr2-agent version

The server, token and CA fingerprint come from "Create registration token" in
the DBR² console (or POST /api/v1/agents/registration-tokens).
`)
}

func enroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	o := agent.EnrollOptions{}
	fs.StringVar(&o.Server, "server", "", "Agent Gateway address (host:port)")
	fs.StringVar(&o.Token, "token", "", "single-use registration token")
	fs.StringVar(&o.CASHA256, "ca-sha256", "", "SHA-256 fingerprint of the DBR² agent CA")
	fs.StringVar(&o.ConfigPath, "config", agent.DefaultConfigPath, "configuration file to write")
	fs.StringVar(&o.StateDir, "state-dir", "/var/lib/dbr2/agent", "state directory (keys, certificates, journal)")
	fs.StringVar(&o.DockerHost, "docker-host", "", "Docker Engine endpoint (default unix:///var/run/docker.sock)")
	fs.StringVar(&o.Hostname, "hostname", "", "host name to register (default: system host name)")
	fs.BoolVar(&o.Force, "force", false, "enroll again even if already enrolled")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.Server == "" || o.Token == "" || o.CASHA256 == "" {
		return errors.New("--server, --token and --ca-sha256 are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, err := agent.Enroll(ctx, o)
	if err != nil {
		return err
	}
	fmt.Printf("Enrolled as agent %s (pending approval).\nConfiguration: %s\n"+
		"Next: an administrator approves this host in the DBR² console; then run `systemctl enable --now dbr2-agent`.\n", id, o.ConfigPath)
	return nil
}

func configFlag(name string, args []string) (string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	path := fs.String("config", agent.DefaultConfigPath, "configuration file")
	return *path, fs.Parse(args)
}

func run(args []string) error {
	path, err := configFlag("run", args)
	if err != nil {
		return err
	}
	cfg, err := agent.LoadConfig(path)
	if err != nil {
		return err
	}
	log := obs.Setup(cfg.LogLevel, "json", binary, version.Of(version.Agent))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var rt runtime.ContainerRuntime
	if d, err := runtime.NewDocker(cfg.DockerHost); err != nil {
		// The agent must survive Docker being absent or broken (ADR-0006).
		log.Error("docker runtime unavailable; running without discovery", "err", err)
	} else {
		rt = d
		defer d.Close()
	}
	a, err := agent.New(cfg, rt, log)
	if err != nil {
		return err
	}
	log.Info("dbr2-agent starting", "agent_id", a.ID(), "server", cfg.Server, "certificate_not_after", a.CertNotAfter())
	err = a.Run(ctx)
	log.Info("dbr2-agent stopped")
	return err
}

func status(args []string) error {
	path, err := configFlag("status", args)
	if err != nil {
		return err
	}
	cfg, err := agent.LoadConfig(path)
	if err != nil {
		return err
	}
	a, err := agent.New(cfg, nil, obs.NewLogger(os.Stderr, "error", "text", binary, version.Of(version.Agent)))
	if err != nil {
		return err
	}
	defer a.Journal().Close()
	fmt.Printf("agent id:     %s\nserver:       %s\nversion:      %s\ncertificate:  expires %s (%s)\nstate dir:    %s\njournal:      %v\n",
		a.ID(), cfg.Server, version.Of(version.Agent), a.CertNotAfter().Format(time.RFC3339),
		time.Until(a.CertNotAfter()).Round(time.Hour), cfg.StateDir, a.Journal().Counts())
	if d, err := runtime.NewDocker(cfg.DockerHost); err == nil {
		defer d.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if v, err := d.Ping(ctx); err == nil {
			fmt.Printf("docker:       reachable (engine %s)\n", v)
		} else {
			fmt.Printf("docker:       UNREACHABLE: %v\n", err)
		}
	}
	return nil
}
