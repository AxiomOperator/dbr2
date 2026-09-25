// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/AxiomOperator/dbr2/internal/obs"
)

// Invocation is one Kopia command run as a child process.
type Invocation struct {
	// Args are the Kopia arguments after the global flags the runner adds
	// (--config-file, --no-persist-credentials). They are logged: never put a
	// secret here.
	Args []string
	// SecretArgs are appended in the child's memory only (see secretArgsEnv).
	SecretArgs []string
	// Env adds environment variables (e.g. KOPIA_SERVER_PASSWORD).
	Env map[string]string
	// Secrets are scrubbed from any output that is logged or returned.
	Secrets []string
}

// Runner runs one-shot Kopia commands.
type Runner interface {
	Run(ctx context.Context, inv Invocation) (stdout []byte, err error)
}

// ExecRunner runs Kopia as `<Exe> kopia …` child processes against the
// reposerver's direct repository connection.
type ExecRunner struct {
	Exe        string // path of the dbr2-reposerver binary (os.Executable)
	ConfigFile string // <state>/kopia/repository.config
	HomeDir    string // HOME for the child (Kopia must not write elsewhere)
	// Password returns the repository password ("" before initialization).
	Password func() string
	Log      *slog.Logger
}

// Command builds the child process for inv. The caller owns starting and
// waiting; stdout/stderr are left unset.
func (r *ExecRunner) Command(ctx context.Context, inv Invocation, password string) *exec.Cmd {
	args := append([]string{KopiaSubcommand, "--config-file=" + r.ConfigFile, "--no-persist-credentials"}, inv.Args...)
	cmd := exec.CommandContext(ctx, r.Exe, args...)
	cmd.Env = childEnv(r.HomeDir, password, inv)
	// The Kopia child must never outlive the reposerver.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	return cmd
}

// Run runs inv to completion and returns its stdout. On failure the error
// carries the (scrubbed) tail of stderr.
func (r *ExecRunner) Run(ctx context.Context, inv Invocation) ([]byte, error) {
	pw := ""
	if r.Password != nil {
		pw = r.Password()
	}
	cmd := r.Command(ctx, inv, pw)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	secrets := append([]string{pw}, inv.Secrets...)
	secrets = append(secrets, inv.SecretArgs...)
	r.Log.Debug("kopia command", "args", strings.Join(inv.Args, " "))
	err := cmd.Run()
	if msg := strings.TrimSpace(Scrub(stderr.String(), secrets...)); msg != "" {
		r.Log.Debug("kopia command output", "args", strings.Join(inv.Args, " "), "stderr", tail(msg, 4000))
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("kopia %s: %w", commandName(inv.Args), ctx.Err())
		}
		return nil, &KopiaError{Command: commandName(inv.Args), Err: err, Stderr: tail(strings.TrimSpace(Scrub(stderr.String(), secrets...)), 2000)}
	}
	return stdout.Bytes(), nil
}

// KopiaError is a failed Kopia child command.
type KopiaError struct {
	Command string
	Err     error
	Stderr  string
}

func (e *KopiaError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("kopia %s: %v: %s", e.Command, e.Err, e.Stderr)
	}
	return fmt.Sprintf("kopia %s: %v", e.Command, e.Err)
}

func (e *KopiaError) Unwrap() error { return e.Err }

// childEnv returns the environment for a Kopia child: the parent's
// environment minus DBR² secrets and inherited KOPIA_* settings, plus the
// repository password (KOPIA_PASSWORD, never argv) and inv.Env.
func childEnv(home, password string, inv Invocation) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "KOPIA_") || strings.HasPrefix(k, "DBR2_") || k == "HOME" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "KOPIA_CHECK_FOR_UPDATES=false", "KOPIA_USE_KEYRING=false")
	if password != "" {
		env = append(env, "KOPIA_PASSWORD="+password)
	}
	for k, v := range inv.Env {
		env = append(env, k+"="+v)
	}
	if len(inv.SecretArgs) > 0 {
		b, _ := json.Marshal(inv.SecretArgs)
		env = append(env, secretArgsEnv+"="+string(b))
	}
	return env
}

// Scrub replaces every occurrence of the given secrets in s.
func Scrub(s string, secrets ...string) string {
	for _, sec := range secrets {
		// A secret arg is "--flag=value": scrub the value part.
		if i := strings.Index(sec, "="); strings.HasPrefix(sec, "--") && i > 0 {
			sec = sec[i+1:]
		}
		if len(sec) >= 4 {
			s = strings.ReplaceAll(s, sec, obs.Redacted)
		}
	}
	return s
}

func commandName(args []string) string {
	var parts []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(parts) == 3 {
			break
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
