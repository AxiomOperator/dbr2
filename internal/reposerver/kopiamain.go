// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/alecthomas/kingpin/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/kopia/kopia/cli"
	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/logging"
)

// KopiaSubcommand is the hidden subcommand of dbr2-reposerver that runs
// Kopia's public `cli` package in-process (ADR-0007 amendment):
//
//	dbr2-reposerver kopia <kopia args…>
//
// The reposerver never runs Kopia commands in its own process: the `cli`
// package can call os.Exit and keeps global state, so every Kopia command
// (the long-running `server start` and the one-shot admin commands) runs as a
// child process of the same binary.
const KopiaSubcommand = "kopia"

// secretArgsEnv carries arguments that must not appear on the child's
// command line (world-readable via /proc/<pid>/cmdline), e.g.
// `--user-password=…`. It is a JSON array appended to argv in-process and
// removed from the environment before Kopia runs.
const secretArgsEnv = "DBR2_KOPIA_SECRET_ARGS" // gitleaks:allow (env var name, not a secret)

// KopiaMain implements the `kopia` subcommand. It does not return when Kopia
// exits with an error (Kopia calls os.Exit).
func KopiaMain(args []string) {
	if raw, ok := os.LookupEnv(secretArgsEnv); ok {
		_ = os.Unsetenv(secretArgsEnv)
		var extra []string
		if err := json.Unmarshal([]byte(raw), &extra); err != nil {
			fmt.Fprintln(os.Stderr, "dbr2-reposerver kopia: invalid", secretArgsEnv)
			os.Exit(2)
		}
		args = append(args, extra...)
	}
	// Record the real Kopia version in the repository format blob and
	// client handshakes instead of the embedding binary's version.
	if v := KopiaVersion(); v != "" {
		repo.BuildVersion = v
	}
	app := cli.NewApp()
	// Upstream main attaches internal/logfile (not importable); without a
	// logger factory Kopia's errors and server messages are silently
	// dropped. Log INFO and above to stderr, which the parent forwards.
	zl := zap.New(zapcore.NewCore(zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
		MessageKey: "msg", LevelKey: "level", NameKey: "logger",
		EncodeLevel: zapcore.CapitalLevelEncoder, EncodeName: zapcore.FullNameEncoder,
		ConsoleSeparator: " ",
	}), zapcore.Lock(os.Stderr), zapcore.InfoLevel))
	app.SetLoggerFactory(func(module string) logging.Logger { return zl.Named(module).Sugar() }, io.Discard)
	kp := kingpin.New("dbr2-reposerver kopia", "Embedded Kopia "+KopiaVersion()+" (DBR² reposerver)")
	kp.ErrorWriter(os.Stderr)
	kp.UsageWriter(os.Stdout)
	app.Attach(kp)
	kingpin.MustParse(kp.Parse(args))
}

// KopiaVersion returns the linked Kopia module version without the "v"
// prefix (e.g. "0.23.1"), or "" if build information is unavailable.
func KopiaVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, d := range bi.Deps {
		if d.Path == "github.com/kopia/kopia" {
			if d.Replace != nil {
				d = d.Replace
			}
			return strings.TrimPrefix(d.Version, "v")
		}
	}
	return ""
}
