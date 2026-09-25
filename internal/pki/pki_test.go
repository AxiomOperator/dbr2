// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
)

func TestAgentIssuanceAndMutualTLSVerification(t *testing.T) {
	ca, keyDER, err := NewCA("DBR² Agent CA")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := LoadCA(ca.DER, keyDER); err != nil || again.Fingerprint() != ca.Fingerprint() {
		t.Fatalf("reload: %v", err)
	}
	keyPEM, csr, err := NewAgentKey("host-a")
	if err != nil {
		t.Fatal(err)
	}
	iss, err := ca.IssueAgent(csr, "agent-123")
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(iss.DER)
	if id, err := AgentID(cert); err != nil || id != "agent-123" {
		t.Fatalf("identity: %q %v", id, err)
	}
	if cert.Subject.CommonName != "agent-123" {
		t.Fatal("subject must be set by the CA, not the CSR")
	}
	// The agent certificate verifies for client auth against the CA only.
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("client verify: %v", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Fatal("agent certificate must not be usable as a server certificate")
	}
	// The key pair loads as a TLS certificate.
	if _, err := tls.X509KeyPair(PEMCert(iss.DER), keyPEM); err != nil {
		t.Fatal(err)
	}

	srv, err := ca.ServerCertificate([]string{"gateway.example.lan", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Leaf.Verify(x509.VerifyOptions{Roots: ca.Pool(), DNSName: "gateway.example.lan"}); err != nil {
		t.Fatalf("server verify: %v", err)
	}
	if srv.Leaf.IPAddresses[0].String() != "10.0.0.5" {
		t.Fatal("IP SAN missing")
	}
}

func TestRejectsBadCSRs(t *testing.T) {
	ca, _, _ := NewCA("ca")
	if _, err := ca.IssueAgent([]byte("junk"), "a"); !errors.Is(err, ErrBadCSR) {
		t.Fatalf("junk: %v", err)
	}
	weak, _ := rsa.GenerateKey(rand.Reader, 1024)
	csr, _ := CSR(weak, "h")
	if _, err := ca.IssueAgent(csr, "a"); !errors.Is(err, ErrBadCSR) {
		t.Fatalf("weak rsa: %v", err)
	}
	p224, _ := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	csr, _ = CSR(p224, "h")
	if _, err := ca.IssueAgent(csr, "a"); !errors.Is(err, ErrBadCSR) {
		t.Fatalf("p224: %v", err)
	}
}

func TestCAFingerprintPinning(t *testing.T) {
	ca, _, _ := NewCA("ca")
	other, _, _ := NewCA("ca")
	if err := VerifyCAFingerprint(ca.DER, ca.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	upper := ""
	for i, c := range ca.Fingerprint() {
		if i > 0 && i%2 == 0 {
			upper += ":"
		}
		upper += string(c)
	}
	if err := VerifyCAFingerprint(ca.DER, upper); err != nil {
		t.Fatalf("colon-separated fingerprint rejected: %v", err)
	}
	if err := VerifyCAFingerprint(other.DER, ca.Fingerprint()); err == nil {
		t.Fatal("wrong CA accepted")
	}
	block, _ := pem.Decode(PEMCert(ca.DER))
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("PEM encoding")
	}
}
