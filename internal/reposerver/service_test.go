// SPDX-License-Identifier: Apache-2.0

package reposerver

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"slices"
	"strings"
	"testing"
)

// kopiaDefaults mirrors `server acl enable` (Kopia v0.23.1 auth.DefaultACLs).
func kopiaDefaults() []ACL {
	return []ACL{
		{ID: "c", User: "*@*", Access: "APPEND", Target: map[string]string{"type": "content"}},
		{ID: "g", User: "*@*", Access: "READ", Target: map[string]string{"type": "policy", "policyType": "global"}},
		{ID: "h", User: "*@*", Access: "READ", Target: map[string]string{"type": "policy", "policyType": "host", "hostname": "OWN_HOST"}},
		{ID: "p", User: "*@*", Access: "FULL", Target: map[string]string{"type": "policy", "username": "OWN_USER", "hostname": "OWN_HOST"}},
		{ID: "s", User: "*@*", Access: "FULL", Target: map[string]string{"type": "snapshot", "username": "OWN_USER", "hostname": "OWN_HOST"}},
		{ID: "u", User: "*@*", Access: "FULL", Target: map[string]string{"type": "user", "username": "OWN_USER@OWN_HOST"}},
	}
}

func TestPlanACLsFromDefaults(t *testing.T) {
	cur := kopiaDefaults()
	cur = append(cur, readGrant("agent@b2", "agent", "a1"))
	cur[len(cur)-1].ID = "grant"
	del, add := PlanACLs(cur)
	slices.Sort(del)
	if !slices.Equal(del, []string{"p", "s", "u"}) {
		t.Fatalf("del = %v", del)
	}
	var got []string
	for _, a := range add {
		got = append(got, a.User+" "+a.Access+" "+TargetString(a.Target))
	}
	want := []string{
		"*@* APPEND type=snapshot,hostname=OWN_HOST,username=OWN_USER",
		"*@* READ type=policy,hostname=OWN_HOST,username=OWN_USER",
		"maint@dbr2 FULL type=snapshot",
		"maint@dbr2 FULL type=policy",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("add = %q", got)
	}
	// Applying the plan converges: a second plan is empty.
	var next []ACL
	for _, e := range cur {
		if !slices.Contains(del, e.ID) {
			next = append(next, e)
		}
	}
	next = append(next, add...)
	if d, a := PlanACLs(next); len(d) != 0 || len(a) != 0 {
		t.Fatalf("not idempotent: del=%v add=%v", d, a)
	}
}

func TestIsReadGrant(t *testing.T) {
	g := readGrant("agent@b2", "agent", "a1")
	if !isReadGrant(g) {
		t.Fatal("grant not recognized")
	}
	for _, e := range append(kopiaDefaults(), DesiredACLs...) {
		if isReadGrant(e) {
			t.Fatalf("base entry treated as a read grant: %+v", e)
		}
	}
}

func TestScrub(t *testing.T) {
	got := Scrub("pw=hunter2hunter2 user --user-password=s3cretpass x", "hunter2hunter2", "--user-password=s3cretpass", "")
	if strings.Contains(got, "hunter2") || strings.Contains(got, "s3cret") {
		t.Fatalf("not scrubbed: %q", got)
	}
}

func TestChildEnvNoSecretsInheritedOrInArgs(t *testing.T) {
	t.Setenv("DBR2_INTERNAL_TOKEN", "tok")
	t.Setenv("KOPIA_SERVER_PASSWORD", "leak")
	env := childEnv("/h", "repopw", Invocation{SecretArgs: []string{"--user-password=x"}, Env: map[string]string{"A": "b"}})
	j := strings.Join(env, "\n")
	for _, bad := range []string{"DBR2_INTERNAL_TOKEN", "KOPIA_SERVER_PASSWORD=leak"} {
		if strings.Contains(j, bad) {
			t.Fatalf("child env contains %s", bad)
		}
	}
	for _, want := range []string{"KOPIA_PASSWORD=repopw", "HOME=/h", "A=b", secretArgsEnv + `=["--user-password=x"]`} {
		if !slices.Contains(env, want) {
			t.Fatalf("child env lacks %s", want)
		}
	}
	r := &ExecRunner{Exe: "/bin/true", ConfigFile: "/c"}
	cmd := r.Command(t.Context(), Invocation{Args: []string{"server", "user", "add", "u@h"}, SecretArgs: []string{"--user-password=x"}}, "repopw")
	if strings.Contains(strings.Join(cmd.Args, " "), "password=") || strings.Contains(strings.Join(cmd.Args, " "), "repopw") {
		t.Fatalf("secret on argv: %v", cmd.Args)
	}
}

func TestStateCertAndSecrets(t *testing.T) {
	st := State{Dir: t.TempDir() + "/state"}
	if err := st.Prepare(); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(st.Dir)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode %v", fi.Mode())
	}
	fp, _, err := st.EnsureCert([]string{"dbr2.example.lan", "localhost", "10.0.0.5"})
	if err != nil {
		t.Fatal(err)
	}
	fp2, missing, err := st.EnsureCert([]string{"dbr2.example.lan", "other.example"})
	if err != nil || fp2 != fp || !slices.Equal(missing, []string{"other.example"}) {
		t.Fatalf("cert not stable: %v %v %v", fp2 == fp, missing, err)
	}
	b, _ := os.ReadFile(st.CertFile())
	blk, _ := pem.Decode(b)
	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil || Fingerprint(blk.Bytes) != fp || len(fp) != 64 || strings.ToLower(fp) != fp {
		t.Fatalf("fingerprint mismatch: %v", err)
	}
	if c.VerifyHostname("10.0.0.5") != nil || c.VerifyHostname("dbr2.example.lan") != nil {
		t.Fatal("SANs missing")
	}
	if c.NotAfter.Sub(c.NotBefore) < CertValidity {
		t.Fatal("validity too short")
	}
	if fi, _ := os.Stat(st.KeyFile()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", fi.Mode())
	}
	p1, _ := st.EnsureControlPassword()
	p2, _ := st.EnsureControlPassword()
	if p1 == "" || p1 != p2 {
		t.Fatal("control password not stable")
	}
	if fi, _ := os.Stat(st.ControlPasswordFile()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("control password mode %v", fi.Mode())
	}
}

func TestLoopback(t *testing.T) {
	for in, want := range map[string]string{"0.0.0.0:51515": "127.0.0.1:51515", ":51515": "127.0.0.1:51515", "10.1.2.3:1": "10.1.2.3:1", "[::]:5": "127.0.0.1:5"} {
		if got := loopback(in); got != want {
			t.Errorf("loopback(%q) = %q, want %q", in, got, want)
		}
	}
}
