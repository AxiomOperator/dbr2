// SPDX-License-Identifier: Apache-2.0

// Package pki implements the DBR² agent CA (ADR-0001, ADR-0016): an ECDSA
// P-256 root that issues the gateway's server certificate and one client
// certificate per agent. Agent private keys never leave the host (CSR flow).
// Only the standard library is used — no custom cryptography.
package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"
)

// Certificate lifetimes.
const (
	CAValidity     = 10 * 365 * 24 * time.Hour
	AgentValidity  = 90 * 24 * time.Hour
	ServerValidity = 30 * 24 * time.Hour
	clockSkew      = 5 * time.Minute
)

// AgentURIPrefix is the URI SAN scheme carrying the agent identity.
const AgentURIPrefix = "dbr2://agent/"

// CA is a loaded certificate authority.
type CA struct {
	Cert *x509.Certificate
	Key  crypto.Signer
	DER  []byte
}

// NewCA generates a new root CA.
func NewCA(commonName string) (*CA, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{"DBR²"}},
		NotBefore:             now.Add(-clockSkew),
		NotAfter:              now.Add(CAValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	ca, err := LoadCA(der, keyDER)
	return ca, keyDER, err
}

// LoadCA parses a CA certificate and PKCS#8 private key.
func LoadCA(certDER, keyDER []byte) (*CA, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(keyDER)
	if err != nil {
		return nil, err
	}
	signer, ok := k.(crypto.Signer)
	if !ok || !cert.IsCA {
		return nil, errors.New("pki: invalid CA material")
	}
	return &CA{Cert: cert, Key: signer, DER: certDER}, nil
}

// Pool returns a cert pool containing the CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.Cert)
	return p
}

// Fingerprint returns the SHA-256 fingerprint of the CA certificate (hex).
func (ca *CA) Fingerprint() string { return Fingerprint(ca.DER) }

// Fingerprint returns the lowercase hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ServerCertificate issues a TLS server certificate for the gateway.
func (ca *CA) ServerCertificate(hostnames []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := randSerial()
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "dbr2-agent-gateway"},
		NotBefore:    now.Add(-clockSkew),
		NotAfter:     now.Add(ServerValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hostnames {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, key.Public(), ca.Key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.DER}, PrivateKey: key, Leaf: leaf}, nil
}

// Issued is an issued agent certificate.
type Issued struct {
	DER         []byte
	Serial      string // hex
	Fingerprint string
	NotBefore   time.Time
	NotAfter    time.Time
}

// ErrBadCSR is returned for unacceptable CSRs.
var ErrBadCSR = errors.New("pki: invalid certificate signing request")

// IssueAgent signs an agent CSR. The subject and SANs are set by the CA (the
// CSR only contributes its public key), so an agent cannot claim another
// identity.
func (ca *CA) IssueAgent(csrDER []byte, agentID string) (*Issued, error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadCSR, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrBadCSR, err)
	}
	if err := acceptableKey(csr.PublicKey); err != nil {
		return nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(AgentURIPrefix + agentID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: agentID, OrganizationalUnit: []string{"dbr2-agent"}},
		URIs:         []*url.URL{u},
		NotBefore:    now.Add(-clockSkew),
		NotAfter:     now.Add(AgentValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, csr.PublicKey, ca.Key)
	if err != nil {
		return nil, err
	}
	return &Issued{DER: der, Serial: serial.Text(16), Fingerprint: Fingerprint(der), NotBefore: tmpl.NotBefore, NotAfter: tmpl.NotAfter}, nil
}

func acceptableKey(pub any) error {
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if k.Curve == elliptic.P256() || k.Curve == elliptic.P384() {
			return nil
		}
	case ed25519.PublicKey:
		return nil
	case *rsa.PublicKey:
		if k.N.BitLen() >= 3072 {
			return nil
		}
	}
	return fmt.Errorf("%w: unsupported or weak public key", ErrBadCSR)
}

// AgentID extracts the agent identity from a verified client certificate.
func AgentID(cert *x509.Certificate) (string, error) {
	for _, u := range cert.URIs {
		if s := u.String(); strings.HasPrefix(s, AgentURIPrefix) {
			return strings.TrimPrefix(s, AgentURIPrefix), nil
		}
	}
	return "", errors.New("pki: certificate carries no agent identity")
}

// SerialHex returns the certificate serial in the format stored in the DB.
func SerialHex(cert *x509.Certificate) string { return cert.SerialNumber.Text(16) }

// NewAgentKey generates an agent key and CSR (agent side).
func NewAgentKey(hostname string) (keyPEM, csrDER []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	csrDER, err = CSR(key, hostname)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), csrDER, nil
}

// CSR builds a CSR for an existing key (used for renewal).
func CSR(key crypto.Signer, hostname string) ([]byte, error) {
	return x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: hostname},
	}, key)
}

// VerifyCAFingerprint checks a CA certificate against the pinned fingerprint.
func VerifyCAFingerprint(caDER []byte, want string) error {
	want = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(want), ":", ""))
	if got := Fingerprint(caDER); got != want {
		return fmt.Errorf("pki: CA fingerprint mismatch (got %s, want %s)", got, want)
	}
	c, err := x509.ParseCertificate(caDER)
	if err != nil {
		return err
	}
	if !c.IsCA {
		return errors.New("pki: pinned certificate is not a CA")
	}
	return nil
}

// PEMCert encodes a DER certificate as PEM.
func PEMCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func randSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}
