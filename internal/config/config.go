// SPDX-License-Identifier: Apache-2.0

// Package config loads DBR² service configuration from environment variables.
//
// Every secret can be supplied directly (DBR2_X) or through a file
// (DBR2_X_FILE), which is how Docker/Compose secrets are mounted.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Log configures structured logging.
type Log struct {
	Level  string `env:"DBR2_LOG_LEVEL" envDefault:"info"`
	Format string `env:"DBR2_LOG_FORMAT" envDefault:"json"` // json | text
}

// Telemetry configures OpenTelemetry. The standard OTEL_* variables
// (e.g. OTEL_EXPORTER_OTLP_ENDPOINT) are honoured by the exporters.
type Telemetry struct {
	Enabled bool `env:"DBR2_OTEL_ENABLED" envDefault:"false"`
}

// Temporal configures the Temporal client (ADR-0009).
type Temporal struct {
	Address   string `env:"DBR2_TEMPORAL_ADDRESS" envDefault:"127.0.0.1:7233"`
	Namespace string `env:"DBR2_TEMPORAL_NAMESPACE" envDefault:"dbr2"`
	TaskQueue string `env:"DBR2_TEMPORAL_TASK_QUEUE" envDefault:"dbr2-control"`
}

// Entra configures Microsoft Entra ID (OIDC). Disabled when TenantID is empty.
type Entra struct {
	TenantID     string `env:"DBR2_ENTRA_TENANT_ID"`
	ClientID     string `env:"DBR2_ENTRA_CLIENT_ID"`
	ClientSecret string // DBR2_ENTRA_CLIENT_SECRET[_FILE]
	GroupsClaim  string `env:"DBR2_ENTRA_GROUPS_CLAIM" envDefault:"groups"`
	// IssuerURL overrides the Entra issuer (used by tests and sovereign clouds).
	IssuerURL string `env:"DBR2_ENTRA_ISSUER_URL"`
}

// Enabled reports whether Entra ID sign-in is configured.
func (e Entra) Enabled() bool { return e.TenantID != "" && e.ClientID != "" }

// Issuer returns the OIDC issuer URL for the tenant.
func (e Entra) Issuer() string {
	if e.IssuerURL != "" {
		return e.IssuerURL
	}
	return "https://login.microsoftonline.com/" + e.TenantID + "/v2.0"
}

// Server is the dbr2-server configuration.
type Server struct {
	HTTPAddr string `env:"DBR2_HTTP_ADDR" envDefault:":8080"`
	// PublicURL is the external base URL of the console (same origin as the
	// API through the web proxy), e.g. https://dbr2.example.lan.
	PublicURL string `env:"DBR2_PUBLIC_URL" envDefault:"http://localhost:3000"`
	// PublicOrigins lists additional origins accepted by the CSRF check.
	PublicOrigins []string `env:"DBR2_PUBLIC_ORIGINS" envSeparator:","`
	// TrustedProxyCIDRs lists proxies whose X-Forwarded-For is believed.
	TrustedProxyCIDRs []string `env:"DBR2_TRUSTED_PROXY_CIDRS" envSeparator:"," envDefault:"127.0.0.1/32,::1/128"`

	DatabaseURL string // DBR2_DATABASE_URL[_FILE]
	AutoMigrate bool   `env:"DBR2_DB_AUTO_MIGRATE" envDefault:"true"`
	ValkeyAddr  string `env:"DBR2_VALKEY_ADDR"`
	// ReadyHTTPChecks adds non-critical readiness checks for other platform
	// services, as name=url pairs (the URL must return HTTP 200), e.g.
	// "proxy=http://proxy:8090/healthz,worker=http://dbr2-worker:8082/healthz".
	ReadyHTTPChecks []string `env:"DBR2_READY_HTTP_CHECKS" envSeparator:","`

	// SecretKey encrypts secrets at rest (e.g. TOTP seeds); 32 bytes, base64.
	SecretKey []byte // DBR2_SECRET_KEY[_FILE]

	DocsPublic    bool          `env:"DBR2_API_DOCS_PUBLIC" envDefault:"false"`
	WebLoginPath  string        `env:"DBR2_WEB_LOGIN_PATH" envDefault:"/login"`
	CookieSecure  bool          `env:"DBR2_COOKIE_SECURE" envDefault:"true"`
	SessionTTL    time.Duration `env:"DBR2_SESSION_TTL" envDefault:"12h"`
	SessionIdle   time.Duration `env:"DBR2_SESSION_IDLE_TIMEOUT" envDefault:"1h"`
	MasterAdmin   string        `env:"DBR2_MASTER_ADMIN_USERNAME" envDefault:"dbr2-admin"`
	InitialPWFile string        `env:"DBR2_MASTER_ADMIN_INITIAL_PASSWORD_FILE" envDefault:"/var/lib/dbr2/master-admin-initial-password"`

	// Agent Gateway (ADR-0001): mTLS gRPC listener agents connect to.
	GatewayAddr string `env:"DBR2_GATEWAY_ADDR" envDefault:":8443"`
	// GatewayHostnames are the names/IPs in the gateway's server certificate
	// (what agents put in --server). The container hostname is always added.
	GatewayHostnames []string `env:"DBR2_GATEWAY_HOSTNAMES" envSeparator:"," envDefault:"localhost"`
	// GatewayPublicAddress is the host:port shown in agent join commands
	// (default: DBR2_PUBLIC_URL's host with port 8443).
	GatewayPublicAddress string `env:"DBR2_GATEWAY_PUBLIC_ADDRESS"`
	// ControlAddr is the internal listener dbr2-worker dispatches through.
	ControlAddr string `env:"DBR2_CONTROL_ADDR" envDefault:":9090"`
	// InternalToken authenticates dbr2-worker on the control listener
	// (DBR2_INTERNAL_TOKEN[_FILE]); the control listener is disabled without it.
	InternalToken string

	Entra     Entra
	Temporal  Temporal
	Log       Log
	Telemetry Telemetry

	trustedProxies []netip.Prefix
	httpChecks     []HTTPCheck
}

// HTTPCheck is a named readiness probe URL.
type HTTPCheck struct {
	Name string
	URL  string
}

// HTTPChecks returns the parsed DBR2_READY_HTTP_CHECKS entries.
func (s *Server) HTTPChecks() []HTTPCheck { return s.httpChecks }

// TrustedProxies returns the parsed proxy CIDRs.
func (s *Server) TrustedProxies() []netip.Prefix { return s.trustedProxies }

// LoadServer reads and validates the server configuration.
func LoadServer() (*Server, error) {
	var c Server
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	var err error
	if c.DatabaseURL, err = secret("DBR2_DATABASE_URL", true); err != nil {
		return nil, err
	}
	if c.Entra.ClientSecret, err = secret("DBR2_ENTRA_CLIENT_SECRET", false); err != nil {
		return nil, err
	}
	if c.InternalToken, err = secret("DBR2_INTERNAL_TOKEN", false); err != nil {
		return nil, err
	}
	key, err := secret("DBR2_SECRET_KEY", true)
	if err != nil {
		return nil, err
	}
	if c.SecretKey, err = base64.StdEncoding.DecodeString(key); err != nil || len(c.SecretKey) != 32 {
		return nil, errors.New("config: DBR2_SECRET_KEY must be 32 bytes, base64-encoded (e.g. `openssl rand -base64 32`)")
	}
	return &c, c.validate()
}

// LoadDatabaseOnly reads just what offline admin commands need.
func LoadDatabaseOnly() (*Server, error) {
	var c Server
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	var err error
	c.DatabaseURL, err = secret("DBR2_DATABASE_URL", true)
	return &c, err
}

func (c *Server) validate() error {
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("config: DBR2_PUBLIC_URL %q must be an absolute http(s) URL", c.PublicURL)
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	for _, cidr := range c.TrustedProxyCIDRs {
		p, err := netip.ParsePrefix(strings.TrimSpace(cidr))
		if err != nil {
			return fmt.Errorf("config: DBR2_TRUSTED_PROXY_CIDRS: %w", err)
		}
		c.trustedProxies = append(c.trustedProxies, p)
	}
	for _, raw := range c.ReadyHTTPChecks {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		name, u, ok := strings.Cut(raw, "=")
		pu, err := url.Parse(u)
		if !ok || name == "" || err != nil || (pu.Scheme != "http" && pu.Scheme != "https") || pu.Host == "" {
			return fmt.Errorf("config: DBR2_READY_HTTP_CHECKS entry %q must be name=http(s)://host/path", raw)
		}
		c.httpChecks = append(c.httpChecks, HTTPCheck{Name: strings.TrimSpace(name), URL: u})
	}
	if c.GatewayPublicAddress == "" {
		u, _ := url.Parse(c.PublicURL)
		c.GatewayPublicAddress = u.Hostname() + ":8443"
	}
	if c.InternalToken != "" && len(c.InternalToken) < 32 {
		return errors.New("config: DBR2_INTERNAL_TOKEN must be at least 32 characters")
	}
	if !strings.HasPrefix(c.WebLoginPath, "/") {
		return errors.New("config: DBR2_WEB_LOGIN_PATH must start with /")
	}
	if c.Entra.TenantID != "" && (c.Entra.ClientID == "" || c.Entra.ClientSecret == "") {
		return errors.New("config: Entra ID requires DBR2_ENTRA_CLIENT_ID and DBR2_ENTRA_CLIENT_SECRET[_FILE]")
	}
	if c.SessionTTL <= 0 || c.SessionIdle <= 0 {
		return errors.New("config: session TTL and idle timeout must be positive")
	}
	return nil
}

// AllowedOrigins returns the origins accepted by the CSRF check.
func (c *Server) AllowedOrigins() []string {
	out := []string{c.PublicURL}
	for _, o := range c.PublicOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// Worker is the dbr2-worker configuration.
type Worker struct {
	Temporal   Temporal
	Log        Log
	Telemetry  Telemetry
	HealthAddr string `env:"DBR2_HEALTH_ADDR" envDefault:":8082"`
	// GatewayControlAddr is dbr2-server's internal control listener.
	GatewayControlAddr string `env:"DBR2_GATEWAY_CONTROL_ADDR" envDefault:"127.0.0.1:9090"`
	// InternalToken authenticates the worker to the gateway and to the
	// reposerver management APIs (DBR2_INTERNAL_TOKEN[_FILE]).
	InternalToken string
	// StateDir holds the maint@dbr2 Kopia client config and cache
	// (disposable; the maint password is only ever in memory).
	StateDir string `env:"DBR2_WORKER_STATE_DIR" envDefault:"/var/lib/dbr2/worker"`
	// OrphanGrace keeps uncommitted components before garbage collection.
	OrphanGrace time.Duration `env:"DBR2_ORPHAN_GRACE" envDefault:"168h"`
	// OrphanGCCron schedules orphan garbage collection (Temporal schedule).
	OrphanGCCron string `env:"DBR2_ORPHAN_GC_CRON" envDefault:"30 3 * * *"`
}

// LoadWorker reads the worker configuration.
func LoadWorker() (*Worker, error) {
	var c Worker
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	var err error
	c.InternalToken, err = secret("DBR2_INTERNAL_TOKEN", false)
	return &c, err
}

// RepoServer is the dbr2-reposerver configuration (ADR-0002).
type RepoServer struct {
	// Path is the Repository storage path (the NFS mount in production).
	Path string `env:"DBR2_REPOSERVER_PATH" envDefault:"/mnt/dbr2-repo"`
	// ExpectFSType is the required filesystem type of the mount ("nfs4" in
	// production). "any" accepts any mounted filesystem (development mock).
	ExpectFSType string `env:"DBR2_REPOSERVER_EXPECT_FSTYPE" envDefault:"nfs4"`
	// RequireMountPoint requires Path to be a mount point (always true in
	// production; the dev mock bind-mounts a directory there).
	RequireMountPoint bool   `env:"DBR2_REPOSERVER_REQUIRE_MOUNTPOINT" envDefault:"true"`
	RepositoryID      string `env:"DBR2_REPOSITORY_ID,required"`
	// InitIfEmpty writes the sentinel on an empty, correctly mounted path at
	// start-up. Development profile only; production initializes explicitly.
	InitIfEmpty      bool          `env:"DBR2_REPOSERVER_INIT_IF_EMPTY" envDefault:"false"`
	HealthAddr       string        `env:"DBR2_HEALTH_ADDR" envDefault:":8081"`
	WatchdogInterval time.Duration `env:"DBR2_REPOSERVER_WATCHDOG_INTERVAL" envDefault:"15s"`
	WatchdogTimeout  time.Duration `env:"DBR2_REPOSERVER_WATCHDOG_TIMEOUT" envDefault:"10s"`
	// StateDir holds the repository password, the Kopia config and cache,
	// the TLS key pair and the server control password (0700).
	StateDir string `env:"DBR2_REPOSERVER_STATE_DIR" envDefault:"/var/lib/dbr2/reposerver"`
	// KopiaAddr is the Kopia repository server's HTTPS listen address.
	KopiaAddr string `env:"DBR2_REPOSERVER_KOPIA_ADDR" envDefault:"0.0.0.0:51515"`
	// TLSNames are extra DNS names / IPs for the self-signed Kopia server
	// certificate (localhost and the container hostname are always added).
	// Clients pin the certificate's SHA-256 fingerprint.
	TLSNames []string `env:"DBR2_REPOSERVER_TLS_NAMES" envSeparator:","`
	// MgmtAddr is the internal management API listener (never published).
	MgmtAddr string `env:"DBR2_REPOSERVER_MGMT_ADDR" envDefault:":8091"`
	// InternalToken authenticates dbr2-server/dbr2-worker on the management
	// API (DBR2_INTERNAL_TOKEN[_FILE]); the API is disabled without it.
	InternalToken string
	Log           Log
	Telemetry     Telemetry
}

// LoadRepoServer reads the reposerver configuration.
func LoadRepoServer() (*RepoServer, error) {
	var c RepoServer
	if err := env.Parse(&c); err != nil {
		return nil, err
	}
	var err error
	if c.InternalToken, err = secret("DBR2_INTERNAL_TOKEN", false); err != nil {
		return nil, err
	}
	if c.InternalToken != "" && len(c.InternalToken) < 32 {
		return nil, errors.New("config: DBR2_INTERNAL_TOKEN must be at least 32 characters")
	}
	return &c, nil
}

// secret reads NAME, or the file named by NAME_FILE.
func secret(name string, required bool) (string, error) {
	v := os.Getenv(name)
	if f := os.Getenv(name + "_FILE"); f != "" {
		if v != "" {
			return "", fmt.Errorf("config: set only one of %s and %s_FILE", name, name)
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return "", fmt.Errorf("config: read %s_FILE: %w", name, err)
		}
		v = strings.TrimSpace(string(b))
	}
	if required && v == "" {
		return "", fmt.Errorf("config: %s (or %s_FILE) is required", name, name)
	}
	return v, nil
}
