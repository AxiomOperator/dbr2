// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/pki"
	"github.com/AxiomOperator/dbr2/internal/version"
)

// EnrollOptions are the `dbr2-agent enroll` arguments.
type EnrollOptions struct {
	Server     string // gateway host:port
	Token      string // single-use registration token
	CASHA256   string // pinned CA fingerprint (from the console)
	ConfigPath string
	StateDir   string
	DockerHost string
	Hostname   string
	Force      bool
}

// Enroll registers this host with the gateway. Trust bootstrap: the CA is
// fetched over TLS without verification and accepted only if its SHA-256
// fingerprint matches the one the administrator supplied; every later
// connection verifies the gateway against that CA.
func Enroll(ctx context.Context, o EnrollOptions) (string, error) {
	cfg := &Config{Server: o.Server, StateDir: o.StateDir, DockerHost: o.DockerHost}
	cfg.Defaults()
	if _, err := os.Stat(cfg.path(certFile)); err == nil && !o.Force {
		return "", fmt.Errorf("already enrolled (%s exists); use --force to enroll again", cfg.path(certFile))
	}
	if o.Hostname == "" {
		o.Hostname, _ = os.Hostname()
	}
	host, _, err := net.SplitHostPort(o.Server)
	if err != nil {
		return "", fmt.Errorf("--server must be host:port: %w", err)
	}

	caDER, err := fetchCA(ctx, o.Server)
	if err != nil {
		return "", err
	}
	if err := pki.VerifyCAFingerprint(caDER, o.CASHA256); err != nil {
		return "", err
	}
	caCert, _ := x509.ParseCertificate(caDER)
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	keyPEM, csr, err := pki.NewAgentKey(o.Hostname)
	if err != nil {
		return "", err
	}
	conn, err := grpc.NewClient(o.Server, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS13,
	})))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := agentv1.NewEnrollmentServiceClient(conn).Enroll(cctx, &agentv1.EnrollRequest{
		Token: o.Token, CsrDer: csr, Hostname: o.Hostname, AgentVersion: version.Of(version.Agent),
		ProtocolVersion: version.Of(version.AgentProtocol), OsRelease: OSRelease(), Architecture: goruntime.GOARCH,
	})
	if err != nil {
		return "", fmt.Errorf("enroll: %w", err)
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return "", err
	}
	if err := writeFileAtomic(cfg.path(keyFile), keyPEM, 0o600); err != nil {
		return "", err
	}
	if err := writeFileAtomic(cfg.path(certFile), pki.PEMCert(resp.CertificateDer), 0o644); err != nil {
		return "", err
	}
	if err := writeFileAtomic(cfg.path(caFile), pki.PEMCert(resp.CaCertificateDer), 0o644); err != nil {
		return "", err
	}
	if err := cfg.Save(o.ConfigPath); err != nil {
		return "", err
	}
	return resp.AgentId, nil
}

func fetchCA(ctx context.Context, server string) ([]byte, error) {
	// Verification is deliberately skipped here: the CA is accepted only if
	// its fingerprint matches the pinned value (checked by the caller).
	conn, err := grpc.NewClient(server, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13, //nolint:gosec // pinned fingerprint verified by caller
	})))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r, err := agentv1.NewEnrollmentServiceClient(conn).GetCA(cctx, &agentv1.GetCARequest{})
	if err != nil {
		return nil, fmt.Errorf("fetch CA from %s: %w", server, err)
	}
	if len(r.CaCertificateDer) == 0 {
		return nil, errors.New("gateway returned no CA certificate")
	}
	return r.CaCertificateDer, nil
}

// OSRelease returns PRETTY_NAME from /etc/os-release.
func OSRelease() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return goruntime.GOOS
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return goruntime.GOOS
}
