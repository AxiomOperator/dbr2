// SPDX-License-Identifier: Apache-2.0

package version

import "testing"

func TestGeneratedVersionsAreValid(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOfUsesBuildExceptForContracts(t *testing.T) {
	old := Build
	t.Cleanup(func() { Build = old })
	Build = "42"

	for _, c := range []string{Server, Worker, Web, Platform} {
		if got := Of(c); !IsValid(got) || got[len(got)-3:] != ".42" {
			t.Errorf("Of(%s) = %q, want build 42", c, got)
		}
	}
	for _, c := range []string{API, AgentProtocol, ManifestSchema, DBSchema} {
		if got := Of(c); !IsValid(got) || got[len(got)-2:] != ".0" {
			t.Errorf("Of(%s) = %q, want contract build 0", c, got)
		}
	}
}

func TestValidateRejectsBadBuild(t *testing.T) {
	old := Build
	t.Cleanup(func() { Build = old })
	Build = "12a"
	if Validate() == nil {
		t.Fatal("expected error for malformed build number")
	}
}

func TestAllCoversEveryComponent(t *testing.T) {
	if got, want := len(All()), 11; got != want {
		t.Fatalf("All() has %d components, want %d (ADR-0015)", got, want)
	}
}
