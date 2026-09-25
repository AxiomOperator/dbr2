// SPDX-License-Identifier: Apache-2.0

package rbac

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAdministratorHasEverything(t *testing.T) {
	set := Resolve([]Role{Administrator})
	for _, p := range All {
		if !set.Has(p) {
			t.Errorf("administrator lacks %s", p)
		}
	}
}

func TestSensitivePermissionsAreRestricted(t *testing.T) {
	for _, r := range []Role{RestoreOperator, ApplicationOperator, Auditor, ReadOnly} {
		set := Resolve([]Role{r})
		for _, p := range []Permission{RestoreProduction, SecretsRead, UserManage, BackupDelete} {
			if set.Has(p) {
				t.Errorf("role %s must not have %s", r, p)
			}
		}
	}
	if !Resolve([]Role{BackupAdministrator}).Has(RestoreProduction) {
		t.Error("backup administrator should hold restore.production")
	}
}

func TestRolesMatchMigrationConstraint(t *testing.T) {
	sql, err := os.ReadFile("../../db/migrations/00001_foundations.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Every role defined in code must be allowed by the user_roles CHECK and
	// vice versa, so the database and the code cannot drift.
	m := regexp.MustCompile(`(?s)CREATE TABLE user_roles.*?CHECK \(role IN \((.*?)\)\)`).FindSubmatch(sql)
	if m == nil {
		t.Fatal("user_roles role CHECK not found")
	}
	inSQL := map[string]bool{}
	for _, v := range regexp.MustCompile(`'([a-z_]+)'`).FindAllStringSubmatch(string(m[1]), -1) {
		inSQL[v[1]] = true
	}
	for _, info := range Roles() {
		if !inSQL[string(info.Role)] {
			t.Errorf("role %s missing from migration CHECK", info.Role)
		}
		delete(inSQL, string(info.Role))
	}
	if len(inSQL) > 0 {
		t.Errorf("migration allows undefined roles: %v", inSQL)
	}
}

func TestPermissionNamesAreWellFormed(t *testing.T) {
	re := regexp.MustCompile(`^[a-z]+\.[a-z]+$`)
	seen := map[Permission]bool{}
	for _, p := range All {
		if !re.MatchString(string(p)) || seen[p] {
			t.Errorf("bad or duplicate permission %q", p)
		}
		seen[p] = true
	}
	if !strings.Contains(strings.Join(Resolve([]Role{Auditor}).Sorted(), ","), "audit.read") {
		t.Error("auditor needs audit.read")
	}
}
