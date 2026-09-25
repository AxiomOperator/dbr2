// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func fixture() *Inventory {
	shop := map[string]string{LabelProject: "shop", LabelConfigFiles: "/srv/shop/compose.yaml", LabelWorkingDir: "/srv/shop"}
	svc := func(s string) map[string]string {
		m := map[string]string{LabelService: s}
		for k, v := range shop {
			m[k] = v
		}
		return m
	}
	return &Inventory{
		SchemaVersion: SchemaVersion, CollectedAt: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC),
		Containers: []Container{
			{ID: "c1", Name: "shop-db-1", Image: "postgres:18", ImageID: "sha256:pg", State: "running", Labels: svc("db"),
				Env:    []EnvVar{{Key: "POSTGRES_USER", Value: "shop"}, {Key: "POSTGRES_PASSWORD", Value: "hunter2"}, {Key: "PATH", Value: "/usr/bin"}},
				Mounts: []Mount{{Type: MountVolume, Name: "shop_pgdata", Source: "/var/lib/docker/volumes/shop_pgdata/_data", Destination: "/var/lib/postgresql", RW: true}},
				Networks: []string{"shop_backend"}, RestartPolicy: "unless-stopped"},
			{ID: "c2", Name: "shop-api-1", Image: "shop/api:2.8.1", ImageID: "sha256:api", State: "running", Labels: svc("api"),
				Env: []EnvVar{{Key: "DATABASE_URL", Value: "postgres://shop:hunter2@db/shop"}, {Key: "LOG_LEVEL", Value: "info"}},
				Mounts: []Mount{
					{Type: MountBind, Source: "/srv/shop/uploads", Destination: "/app/uploads", RW: true},
					{Type: MountVolume, Name: "shop_cache", Destination: "/app/cache", RW: true},
					{Type: MountVolume, Name: "shop_archive", Destination: "/archive", RW: true},
				},
				Networks: []string{"shop_backend", "proxy"},
				Ports:    []Port{{ContainerPort: "8080", Protocol: "tcp", HostIP: "0.0.0.0", HostPort: "8080"}},
				Changes: []Change{
					{Path: "/app", Kind: "C"}, {Path: "/app/reports", Kind: "A"}, {Path: "/app/reports/q3.pdf", Kind: "A"},
					{Path: "/app/reports/q4.pdf", Kind: "A"}, {Path: "/app/uploads/a.png", Kind: "A"}, {Path: "/tmp/sess", Kind: "A"},
					{Path: "/var", Kind: "C"}, {Path: "/var/log", Kind: "C"}, {Path: "/var/log/api.log", Kind: "A"},
					{Path: "/var/lib/custom-data/state.db", Kind: "A"}, {Path: "/etc/hostname", Kind: "C"}, {Path: "/old", Kind: "D"},
				}},
			{ID: "c3", Name: "legacy", Image: "legacy:1", ImageID: "sha256:leg", State: "exited",
				Mounts: []Mount{{Type: MountVolume, Name: strings.Repeat("a", 64), Destination: "/data", RW: true}}, Networks: []string{"bridge"}},
			{ID: "c4", Name: "helper-a", Image: "busybox", ImageID: "sha256:bb", State: "running"},
			{ID: "c5", Name: "helper-b", Image: "busybox", ImageID: "sha256:bb", State: "running", ReadOnlyRootfs: true,
				Changes: []Change{{Path: "/data/x", Kind: "A"}}},
			{ID: "c6", Name: "wiki-app-1", Image: "wiki:1", ImageID: "sha256:w", State: "running",
				Labels: map[string]string{LabelProject: "wiki", LabelService: "app", LabelConfigFiles: "/opt/wiki/compose.yaml"}},
		},
		Volumes: []Volume{
			{Name: "shop_pgdata", Driver: "local", Mountpoint: "/var/lib/docker/volumes/shop_pgdata/_data", Labels: map[string]string{LabelProject: "shop", LabelVolume: "pgdata"}},
			{Name: "shop_cache", Driver: "local", Labels: map[string]string{LabelProject: "shop"}},
			{Name: "shop_archive", Driver: "local", Labels: map[string]string{LabelProject: "shop"},
				Options: map[string]string{"type": "nfs", "o": "addr=10.0.0.55,rw", "device": ":/export/archive"}},
			{Name: strings.Repeat("a", 64), Driver: "local"},
		},
		Networks: []Network{
			{Name: "shop_backend", Driver: "bridge", Labels: map[string]string{LabelProject: "shop"}},
			{Name: "proxy", Driver: "bridge"}, {Name: "bridge", Driver: "bridge"},
		},
		Images: []Image{{ID: "sha256:pg", RepoDigests: []string{"postgres@sha256:abc"}, OS: "linux", Architecture: "amd64"}},
		ComposeProjects: []ComposeProject{
			{Name: "shop", WorkingDir: "/srv/shop", ConfigFiles: []File{{Path: "/srv/shop/compose.yaml", Content: "services: {}\n"}}},
			{Name: "wiki", WorkingDir: "/opt/wiki", ConfigFiles: []File{{Path: "/opt/wiki/compose.yaml", Error: "permission denied"}}},
		},
	}
}

func find(apps []Application, key string) *Application {
	for i := range apps {
		if apps[i].Key == key {
			return &apps[i]
		}
	}
	return nil
}

func TestAnalyzeGroupingAndProvenance(t *testing.T) {
	apps := Analyze(fixture(), map[string][]string{"manual:helpers": {"helper-a", "helper-b"}})
	for _, k := range []string{"compose:shop", "compose:wiki", "container:legacy", "manual:helpers"} {
		if find(apps, k) == nil {
			t.Fatalf("missing application %s (have %d)", k, len(apps))
		}
	}
	if find(apps, "container:helper-a") != nil {
		t.Fatal("manually grouped container also listed as standalone")
	}
	shop := find(apps, "compose:shop")
	if shop.Source != SourceOriginal || len(shop.Services) != 2 || shop.WorkingDir != "/srv/shop" {
		t.Fatalf("shop: %+v", shop)
	}
	if w := find(apps, "compose:wiki"); w.Source != SourceReconstructed || !strings.Contains(w.SourceReason, "permission denied") {
		t.Fatalf("wiki provenance: %s %q", w.Source, w.SourceReason)
	}
	if find(apps, "container:legacy").Source != SourceReconstructed {
		t.Fatal("standalone container must be reconstructed")
	}
}

func TestVolumeClassificationAndDependencies(t *testing.T) {
	shop := find(Analyze(fixture(), nil), "compose:shop")
	class := map[string]string{}
	for _, v := range shop.Volumes {
		class[v.Name] = v.Class
	}
	if class["shop_pgdata"] != ClassLocal || class["shop_cache"] != ClassEphemeral || class["shop_archive"] != ClassExternal {
		t.Fatalf("classes: %v", class)
	}
	deps := map[string]bool{}
	for _, d := range shop.Dependencies {
		deps[d.Kind+":"+d.Name] = true
	}
	if !deps[DepExternalNetwork+":proxy"] || !deps[DepNetworkStorage+":shop_archive"] || deps[DepExternalNetwork+":shop_backend"] {
		t.Fatalf("dependencies: %v", deps)
	}
	legacy := find(Analyze(fixture(), nil), "container:legacy")
	if !legacy.Volumes[0].Anonymous || len(legacy.Dependencies) != 0 {
		t.Fatalf("legacy: %+v", legacy)
	}
}

func TestUnprotectedDataDetection(t *testing.T) {
	shop := find(Analyze(fixture(), nil), "compose:shop")
	got := map[string]Unprotected{}
	for _, u := range shop.Unprotected {
		got[u.Path] = u
	}
	if u := got["/app/reports"]; u.Severity != "high" || u.Files != 2 {
		t.Fatalf("/app/reports: %+v (all %+v)", u, shop.Unprotected)
	}
	if got["/var/lib/custom-data"].Severity != "high" {
		t.Fatalf("custom-data missing: %+v", shop.Unprotected)
	}
	if got["/var/log"].Severity != "low" {
		t.Fatalf("logs should be low severity: %+v", shop.Unprotected)
	}
	for _, p := range []string{"/app/uploads", "/tmp", "/etc/hostname", "/app", "/var", "/old", "/app/cache"} {
		if _, ok := got[p]; ok {
			t.Errorf("%s must not be reported (mounted, system, parent dir or deleted)", p)
		}
	}
	if len(find(Analyze(fixture(), map[string][]string{"manual:helpers": {"helper-a", "helper-b"}}), "manual:helpers").Unprotected) != 0 {
		t.Fatal("read-only rootfs container cannot have unprotected writes")
	}
}

func TestMountPointParentsAndCertificatesAreNotHighSeverity(t *testing.T) {
	c := Container{Name: "rs", Mounts: []Mount{{Type: MountBind, Destination: "/mnt/dbr2-repo"}},
		Changes: []Change{{Path: "/mnt", Kind: "C"}, {Path: "/mnt/dbr2-repo", Kind: "A"},
			{Path: "/usr/local/share/ca-certificates/root.crt", Kind: "A"}}}
	got := UnprotectedPaths(c)
	if len(got) != 1 || got[0].Path != "/usr/local/share" || got[0].Severity != "low" {
		t.Fatalf("got %+v", got)
	}
}

func TestImageDigestAndPlatform(t *testing.T) {
	shop := find(Analyze(fixture(), nil), "compose:shop")
	for _, im := range shop.Images {
		if im.Reference == "postgres:18" && (im.Platform != "linux/amd64" || im.Digests[0] != "postgres@sha256:abc") {
			t.Fatalf("image ref: %+v", im)
		}
	}
}

type fakeSealer struct{}

func (fakeSealer) Seal(p []byte, ad string) ([]byte, error) { return append([]byte(ad+"|"), p...), nil }
func (fakeSealer) Open(s []byte, ad string) ([]byte, error) {
	if !bytes.HasPrefix(s, []byte(ad+"|")) {
		return nil, errors.New("wrong ad")
	}
	return s[len(ad)+1:], nil
}

func TestSealRedactReveal(t *testing.T) {
	inv := fixture()
	inv.ComposeProjects[0].EnvFiles = []File{{Path: "/srv/shop/.env", Content: "TZ=UTC\nDB_PASSWORD=hunter2\n"}}
	if err := Seal(inv, fakeSealer{}, SealAD("agent-1")); err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(inv)
	if bytes.Contains(raw, []byte("hunter2")) && !bytes.Contains(raw, []byte("dbr2:inventory:agent-1|")) {
		t.Fatal("plaintext secret stored")
	}
	apps := Analyze(inv, nil)
	if find(apps, "compose:shop").SecretsCount != 3 { // POSTGRES_PASSWORD, DATABASE_URL, .env
		t.Fatalf("secrets count = %d", find(apps, "compose:shop").SecretsCount)
	}
	red := *inv
	red.Containers = append([]Container(nil), inv.Containers...)
	Redact(inv)
	if inv.Containers[0].Env[1].Value != "********" || inv.Containers[0].Env[1].Sealed != nil {
		t.Fatalf("redact: %+v", inv.Containers[0].Env[1])
	}
	inv2 := fixture()
	_ = Seal(inv2, fakeSealer{}, SealAD("agent-1"))
	if err := Reveal(inv2, fakeSealer{}, SealAD("agent-2")); err == nil {
		t.Fatal("reveal under another agent's AD must fail")
	}
	if err := Reveal(inv2, fakeSealer{}, SealAD("agent-1")); err != nil || inv2.Containers[0].Env[1].Value != "hunter2" {
		t.Fatalf("reveal: %v %+v", err, inv2.Containers[0].Env[1])
	}
}

func TestReconstructCompose(t *testing.T) {
	inv := fixture()
	_ = Seal(inv, fakeSealer{}, SealAD("a"))
	wiki := find(Analyze(inv, nil), "compose:shop")
	out, err := Reconstruct(wiki, inv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "# RECONSTRUCTED by DBR²") || strings.Contains(out, "hunter2") {
		t.Fatalf("header/secret:\n%s", out)
	}
	var doc struct {
		Services map[string]struct {
			Image       string            `yaml:"image"`
			Environment map[string]string `yaml:"environment"`
			Volumes     []string          `yaml:"volumes"`
			Networks    []string          `yaml:"networks"`
			Ports       []string          `yaml:"ports"`
			Restart     string            `yaml:"restart"`
		} `yaml:"services"`
		Networks map[string]struct {
			Name     string `yaml:"name"`
			External bool   `yaml:"external"`
		} `yaml:"networks"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("reconstructed compose is not valid YAML: %v\n%s", err, out)
	}
	db, api := doc.Services["db"], doc.Services["api"]
	if db.Image != "postgres:18" || db.Environment["POSTGRES_PASSWORD"] != "${POSTGRES_PASSWORD}" || db.Environment["POSTGRES_USER"] != "shop" {
		t.Fatalf("db: %+v", db)
	}
	if _, ok := db.Environment["PATH"]; ok {
		t.Fatal("image default env copied")
	}
	if db.Volumes[0] != "pgdata:/var/lib/postgresql" || db.Restart != "unless-stopped" {
		t.Fatalf("db volumes/restart: %+v", db)
	}
	if api.Ports[0] != "8080:8080" || !contains(api.Volumes, "/srv/shop/uploads:/app/uploads") {
		t.Fatalf("api: %+v", api)
	}
	if n := doc.Networks["proxy"]; !n.External || n.Name != "proxy" {
		t.Fatalf("external network: %+v", doc.Networks)
	}
	if _, ok := doc.Volumes["pgdata"]; !ok {
		t.Fatalf("top-level volumes: %v", doc.Volumes)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
