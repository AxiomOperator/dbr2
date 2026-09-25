// Command embedded-kopia runs Kopia's public `cli` package in-process, the way
// the upstream `kopia` main does. It proves `dbr2-reposerver` can ship the Kopia
// repository server inside its own binary (`embedded-kopia server start ...`)
// without importing any `internal/...` package - at the cost of driving it
// through CLI arguments instead of a Go API. (Upstream main additionally calls
// internal/logfile.Attach, which we cannot; logs then only go to stderr.)
package main

import (
	"os"

	"github.com/alecthomas/kingpin/v2"

	"github.com/kopia/kopia/cli"
	"github.com/kopia/kopia/repo"
)

func main() {
	app := cli.NewApp()
	kp := kingpin.New("dbr2-reposerver", "DBR² repository server (embedded Kopia "+repo.BuildVersion+")")
	kp.ErrorWriter(os.Stderr)
	kp.UsageWriter(os.Stdout)
	app.Attach(kp)
	kingpin.MustParse(kp.Parse(os.Args[1:]))
}
