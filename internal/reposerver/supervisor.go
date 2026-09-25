// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// StopTimeout is how long the Kopia server gets to exit after SIGTERM
// before it is killed.
const StopTimeout = 20 * time.Second

// Supervisor keeps the long-running `kopia server start` child alive: it
// re-checks the storage guard before every start and restarts the child with
// exponential backoff when it exits.
type Supervisor struct {
	Runner *ExecRunner
	// Args returns the `server start …` invocation (evaluated on every start).
	Invocation func() (Invocation, string, error)
	// GuardCheck is the storage guard, bounded by the watchdog timeout.
	GuardCheck func() error
	// ProbeAddr is a loopback host:port for the readiness probe, which
	// completes a TLS handshake and checks the certificate fingerprint.
	ProbeAddr   string
	Fingerprint string
	Log         *slog.Logger

	MinBackoff, MaxBackoff time.Duration

	mu      sync.Mutex
	started bool
	done    chan struct{}
	running atomic.Bool
	pid     atomic.Int64
	lastErr atomic.Pointer[string]
}

// Running reports whether the Kopia server child is up and accepting
// connections.
func (s *Supervisor) Running() bool { return s.running.Load() }

// LastError is the most recent start/exit error ("" if none).
func (s *Supervisor) LastError() string {
	if p := s.lastErr.Load(); p != nil {
		return *p
	}
	return ""
}

// Start launches the supervision loop once; later calls are no-ops.
func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	s.done = make(chan struct{})
	go s.loop(ctx)
}

// Wait blocks until the loop has stopped the child after ctx was canceled
// (immediately if the loop was never started).
func (s *Supervisor) Wait() {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (s *Supervisor) setErr(err error) {
	if err == nil {
		s.lastErr.Store(nil)
		return
	}
	m := err.Error()
	s.lastErr.Store(&m)
}

func (s *Supervisor) loop(ctx context.Context) {
	defer close(s.done)
	// Pdeathsig is tied to the OS thread that forked the child: keep this
	// goroutine on one thread so the thread outlives the child.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	minB, maxB := s.MinBackoff, s.MaxBackoff
	if minB <= 0 {
		minB = time.Second
	}
	if maxB <= 0 {
		maxB = 30 * time.Second
	}
	backoff := minB
	for ctx.Err() == nil {
		began := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		s.setErr(err)
		if time.Since(began) > 2*time.Minute {
			backoff = minB
		}
		s.Log.Error("kopia server is down; restarting", "err", err, "backoff", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxB)
	}
}

var errExited = errors.New("kopia server exited")

func (s *Supervisor) runOnce(ctx context.Context) error {
	if err := s.GuardCheck(); err != nil {
		return err
	}
	inv, password, err := s.Invocation()
	if err != nil {
		return err
	}
	cmd := s.Runner.Command(context.Background(), inv, password)
	secrets := append([]string{password}, inv.Secrets...)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	go s.forward(pr, secrets)
	if err := cmd.Start(); err != nil {
		pw.Close()
		return err
	}
	s.pid.Store(int64(cmd.Process.Pid))
	s.Log.Info("kopia server started", "pid", cmd.Process.Pid, "args", joinArgs(inv.Args))
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); pw.Close() }()

	probeStop := make(chan struct{})
	go s.probe(probeStop)
	defer func() { close(probeStop); s.running.Store(false); s.pid.Store(0) }()

	select {
	case err := <-exited:
		if err == nil {
			err = errExited
		}
		return err
	case <-ctx.Done():
		s.running.Store(false)
		s.Log.Info("stopping kopia server", "pid", cmd.Process.Pid)
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(StopTimeout):
			s.Log.Warn("kopia server did not stop in time; killing it", "pid", cmd.Process.Pid)
			_ = cmd.Process.Kill()
			<-exited
		}
		s.Log.Info("kopia server stopped")
		return ctx.Err()
	}
}

// probe marks the server running once its port accepts connections and
// keeps re-checking it.
func (s *Supervisor) probe(stop <-chan struct{}) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	fails := 0
	for {
		err := s.handshake()
		if err == nil {
			fails = 0
			if !s.running.Swap(true) {
				s.setErr(nil)
				s.Log.Info("kopia server is accepting connections", "addr", s.ProbeAddr)
				t.Reset(5 * time.Second)
			}
		} else if s.running.Load() {
			if fails++; fails >= 3 {
				s.running.Store(false)
				s.setErr(err)
				s.Log.Error("kopia server stopped accepting connections", "err", err)
				t.Reset(500 * time.Millisecond)
			}
		}
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}

// handshake completes a TLS handshake with the Kopia server (a bare TCP
// probe makes Kopia log a handshake error every time).
func (s *Supervisor) handshake() error {
	d := &net.Dialer{Timeout: 2 * time.Second}
	c, err := tls.DialWithDialer(d, "tcp", s.ProbeAddr, &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // verified by fingerprint below
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 || (s.Fingerprint != "" && Fingerprint(raw[0]) != s.Fingerprint) {
				return errors.New("kopia server presents an unexpected certificate")
			}
			return nil
		},
	})
	if err != nil {
		return err
	}
	return c.Close()
}

// forward logs the Kopia child's output line by line, mapping Kopia's level
// prefix to the slog level.
func (s *Supervisor) forward(r io.Reader, secrets []string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(Scrub(sc.Text(), secrets...))
		lvl := slog.LevelInfo
		for p, l := range map[string]slog.Level{"DEBUG ": slog.LevelDebug, "INFO ": slog.LevelInfo, "WARN ": slog.LevelWarn, "ERROR ": slog.LevelError, "FATAL ": slog.LevelError} {
			if strings.HasPrefix(line, p) {
				line, lvl = strings.TrimSpace(strings.TrimPrefix(line, p)), l
				break
			}
		}
		if line == "" || line == "kopia/cli" {
			continue
		}
		s.Log.Log(context.Background(), lvl, line, "component", "kopia")
	}
	_, _ = io.Copy(io.Discard, r)
}

func joinArgs(a []string) string {
	out := ""
	for i, s := range a {
		if i > 0 {
			out += " "
		}
		out += s
	}
	return out
}
