// SPDX-License-Identifier: Apache-2.0

package escrow

import (
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func TestSealOpenBothRecipients(t *testing.T) {
	a, _ := age.GenerateX25519Identity()
	b, _ := age.GenerateX25519Identity()
	code, err := NewConfirmationCode()
	if err != nil || len(code) != 19 {
		t.Fatal(code, err)
	}
	pkg, err := Seal(Payload{Kind: KindRepositoryPassword, CreatedAt: time.Now(), Secret: "s3cret", ConfirmationCode: code},
		[]string{a.Recipient().String(), b.Recipient().String()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(pkg), "-----BEGIN AGE ENCRYPTED FILE-----") || strings.Contains(string(pkg), "s3cret") {
		t.Fatal("package is not armored ciphertext")
	}
	for _, id := range []age.Identity{a, b} {
		p, err := Open(pkg, id)
		if err != nil || p.Secret != "s3cret" || p.FormatVersion != 1 {
			t.Fatalf("open = %+v, %v", p, err)
		}
		if !CheckCode(strings.ToLower(strings.ReplaceAll(p.ConfirmationCode, "-", " ")), HashCode(code)) {
			t.Fatal("normalized code rejected")
		}
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := Open(pkg, other); err == nil {
		t.Fatal("foreign identity decrypted the package")
	}
	if CheckCode("AAAA-BBBB-CCCC-DDDD", HashCode(code)) || CheckCode(code, nil) {
		t.Fatal("wrong code accepted")
	}
}

func TestRecipients(t *testing.T) {
	if _, err := Seal(Payload{}, nil); err == nil {
		t.Fatal("sealed with no recipients")
	}
	if _, err := ParseRecipient("not-a-key"); err == nil {
		t.Fatal("accepted garbage")
	}
	if _, err := ParseRecipient("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHsKLqeplhpW+uObz5dvMgjz1OxfM/XXUB+VHtZ6isGN"); err != nil {
		t.Fatalf("ssh key rejected: %v", err)
	}
}
