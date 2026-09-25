// Command spike is the DBR² Temporal spike: `spike worker` runs a worker; `spike run` runs all
// scenarios (spawning/killing worker subprocesses) with assertions.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.temporal.io/sdk/client"
	tlog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"
)

func hostPort() string {
	if v := os.Getenv("TEMPORAL_ADDRESS"); v != "" {
		return v
	}
	return "127.0.0.1:17233"
}

func dial(level slog.Level) (client.Client, error) {
	return client.Dial(client.Options{
		HostPort:  hostPort(),
		Namespace: "dbr2",
		Logger:    tlog.NewStructuredLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))),
	})
}

func runWorker() error {
	c, err := dial(slog.LevelInfo)
	if err != nil {
		return err
	}
	defer c.Close()
	w := worker.New(c, TaskQueue, worker.Options{})
	w.RegisterWorkflow(BackupWorkflow)
	w.RegisterWorkflow(RestoreWorkflow)
	w.RegisterWorkflow(ScheduledBackupTrigger)
	w.RegisterActivity(&Activities{Client: c})
	stop := make(chan interface{})
	go func() {
		s := make(chan os.Signal, 1)
		signal.Notify(s, syscall.SIGINT, syscall.SIGTERM)
		<-s
		close(stop)
	}()
	return w.Run(stop)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: spike worker|run [scenario...]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "worker":
		err = runWorker()
	case "run":
		err = runScenarios(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", err)
		os.Exit(1)
	}
}
