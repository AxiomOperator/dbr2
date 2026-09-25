// SPDX-License-Identifier: Apache-2.0

//go:build integration

package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AxiomOperator/dbr2/internal/inventory"
)

// TestDockerDiscovery runs a real Compose project on the local Docker Engine
// and checks the Phase 3 detections end to end (discovery + analysis).
func TestDockerDiscovery(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available (used only to set up the fixture)")
	}
	dir, _ := filepath.Abs("testdata/shop")
	const project = "dbr2test-disc"
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("docker", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	_ = exec.Command("docker", "network", "create", "dbr2test-shared").Run()
	t.Cleanup(func() {
		_ = exec.Command("docker", "compose", "-p", project, "-f", filepath.Join(dir, "compose.yaml"), "down", "-v", "--remove-orphans").Run()
		_ = exec.Command("docker", "network", "rm", "dbr2test-shared").Run()
	})
	run("compose", "-p", project, "-f", "compose.yaml", "up", "-d", "--quiet-pull")
	time.Sleep(2 * time.Second) // let web write its files

	rt, err := NewDocker("")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if v, err := rt.Ping(ctx); err != nil || v == "" {
		t.Fatalf("ping: %q %v", v, err)
	}
	inv, err := rt.Discover(ctx, DiscoverOptions{FilesystemChanges: true})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Host.RootDir == "" || inv.Host.EngineVersion == "" {
		t.Fatalf("host facts: %+v", inv.Host)
	}

	var proj *inventory.ComposeProject
	for i := range inv.ComposeProjects {
		if inv.ComposeProjects[i].Name == project {
			proj = &inv.ComposeProjects[i]
		}
	}
	if proj == nil || len(proj.ConfigFiles) != 1 || proj.ConfigFiles[0].Error != "" || !strings.Contains(proj.ConfigFiles[0].Content, "fixture-secret-123") {
		t.Fatalf("original compose file not collected: %+v", proj)
	}
	if len(proj.EnvFiles) != 1 || !strings.Contains(proj.EnvFiles[0].Content, "API_TOKEN") {
		t.Fatalf(".env not collected: %+v", proj.EnvFiles)
	}

	var app *inventory.Application
	apps := inventory.Analyze(inv, nil)
	for i := range apps {
		if apps[i].Key == "compose:"+project {
			app = &apps[i]
		}
	}
	if app == nil {
		t.Fatal("application not grouped")
	}
	if app.Source != inventory.SourceOriginal || len(app.Services) != 2 {
		t.Fatalf("app: source=%s services=%d", app.Source, len(app.Services))
	}
	class := map[string]string{}
	for _, v := range app.Volumes {
		class[v.Name] = v.Class
	}
	if class[project+"_dbdata"] != inventory.ClassLocal || class[project+"_cache"] != inventory.ClassEphemeral {
		t.Fatalf("volume classes: %v", class)
	}
	if len(app.BindMounts) != 1 || !strings.HasSuffix(app.BindMounts[0].Source, "testdata/shop/uploads") {
		t.Fatalf("bind mounts: %+v", app.BindMounts)
	}
	ext := false
	for _, d := range app.Dependencies {
		ext = ext || (d.Kind == inventory.DepExternalNetwork && d.Name == "dbr2test-shared")
	}
	if !ext {
		t.Fatalf("external network not flagged: %+v", app.Dependencies)
	}
	unprot := false
	for _, u := range app.Unprotected {
		unprot = unprot || (u.Path == "/app/reports" && u.Severity == "high")
		if strings.HasPrefix(u.Path, "/tmp") || strings.HasPrefix(u.Path, "/app/uploads") {
			t.Fatalf("mounted/temp path reported as unprotected: %+v", u)
		}
	}
	if !unprot {
		t.Fatalf("/app/reports not detected as unprotected: %+v", app.Unprotected)
	}
	digest := false
	for _, im := range app.Images {
		digest = digest || (im.Platform == "linux/amd64" && len(im.Digests) > 0)
	}
	if !digest {
		t.Fatalf("image digest/platform missing: %+v", app.Images)
	}
	if os.Getenv("DBR2_TEST_VERBOSE") != "" {
		t.Logf("%+v", app)
	}
}
