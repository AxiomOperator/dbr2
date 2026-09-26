// SPDX-License-Identifier: Apache-2.0

package protection

import (
	"strings"
	"testing"

	"github.com/AxiomOperator/dbr2/internal/inventory"
	"github.com/AxiomOperator/dbr2/internal/manifest"
)

const src, other = "agent-src", "agent-other"

func testManifest() *manifest.Manifest {
	u := uint32(0)
	return &manifest.Manifest{
		RecoveryPointID: "rp_01M3D1ERYCA7VDQAC2N3MWDH9H",
		Application:     manifest.Application{ID: "app-1", Name: "shop", ComposeProject: "shop", WorkingDir: "/srv/shop"},
		Source:          manifest.Source{AgentID: src},
		Images:          []manifest.Image{{Ref: "postgres:18", Digest: "docker.io/library/postgres@sha256:aaa"}, {Ref: "nginx:1", Digest: "nginx@sha256:bbb"}},
		Topology: &manifest.Topology{
			Containers: []manifest.TopologyContainer{
				{Name: "shop-db-1", Service: "db", Image: "postgres:18", State: "running"},
				{Name: "shop-web-1", Service: "web", Image: "nginx:1", State: "running", Ports: []manifest.TopologyPort{{ContainerPort: "80", Protocol: "tcp", HostPort: "8080"}}},
			},
			Networks: []manifest.TopologyNetwork{{Name: "shop_default", Driver: "bridge"}, {Name: "proxy", Driver: "bridge", External: true}},
			Volumes:  []manifest.TopologyVolume{{Name: "shop_data", Driver: "local"}},
		},
		Components: []manifest.Component{
			{Name: "config", Kind: manifest.KindConfig, Required: true, Status: manifest.ComponentSucceeded, SnapshotID: "c"},
			{Name: "volume:shop_data", Kind: manifest.KindVolume, Required: true, Status: manifest.ComponentSucceeded, SnapshotID: "v", VolumeName: "shop_data", OwnerUID: &u},
			{Name: "fsmeta:volume:shop_data", Kind: manifest.KindFSMeta, Parent: "volume:shop_data", Status: manifest.ComponentSucceeded, SnapshotID: "f"},
			{Name: "bind:/srv/shop/conf", Kind: manifest.KindBindMount, Required: true, Status: manifest.ComponentSucceeded, SnapshotID: "b", Path: "/srv/shop/conf"},
			{Name: "image:web", Kind: manifest.KindImage, Status: manifest.ComponentFailed},
		},
	}
}

func ctr(name, project, state string, ports ...string) inventory.Container {
	c := inventory.Container{ID: "id-" + name, Name: "/" + name, State: state, Labels: map[string]string{}}
	if project != "" {
		c.Labels[inventory.LabelProject] = project
	}
	for _, p := range ports {
		c.Ports = append(c.Ports, inventory.Port{HostPort: p, Protocol: "tcp"})
	}
	return c
}

func preview(t *testing.T, target string, inv *inventory.Inventory, remaps []PathRemap, env string) *Preview {
	t.Helper()
	m := testManifest()
	sel, err := selectComponents(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	return computePreview(previewInput{m: m, targetAgentID: target, targetHostname: "h", inv: inv, remaps: remaps, targetAppEnv: env}, sel)
}

func kinds(p *Preview) string {
	var k []string
	for _, c := range p.Collisions {
		k = append(k, c.Kind+":"+c.Name)
	}
	return strings.Join(k, ",")
}

func TestInPlaceRunningIsProduction(t *testing.T) {
	inv := &inventory.Inventory{
		Containers: []inventory.Container{ctr("shop-db-1", "shop", "running"), ctr("shop-web-1", "shop", "running", "8080")},
		Networks:   []inventory.Network{{Name: "shop_default", Labels: map[string]string{inventory.LabelProject: "shop"}}, {Name: "proxy"}},
		Volumes:    []inventory.Volume{{Name: "shop_data", Labels: map[string]string{inventory.LabelProject: "shop"}}},
		Images:     []inventory.Image{{RepoDigests: []string{"postgres@sha256:aaa"}}},
	}
	p := preview(t, src, inv, nil, "")
	if p.Blocked || p.Mode != "in_place" || !p.Production || len(p.stopIDs) != 2 || len(p.CreateContainers) != 0 {
		t.Fatalf("preview = %+v (collisions %s)", p, kinds(p))
	}
	if len(p.Components) != 3 || p.Components[1].Action != "overwrite" {
		t.Fatalf("components %+v", p.Components)
	}
	if p.Images[0].Action != "present" || p.Images[1].Action != "pull" {
		t.Fatalf("images %+v", p.Images)
	}
}

func TestAlternateHostCollisions(t *testing.T) {
	inv := &inventory.Inventory{
		Containers: []inventory.Container{
			ctr("shop-web-1", "blog", "running"),    // name owned by another project
			ctr("other", "blog", "running", "8080"), // port in use
		},
		Networks: []inventory.Network{{Name: "shop_default", Labels: map[string]string{inventory.LabelProject: "blog"}}},
		Volumes:  []inventory.Volume{{Name: "shop_data", Labels: map[string]string{inventory.LabelProject: "blog"}}},
	}
	inv.Containers[1].Mounts = []inventory.Mount{{Type: "bind", Source: "/srv/shop/conf/site"}}
	agent := ctr("dbr2-agent", "", "running")
	agent.Labels[AgentRoleLabel] = "agent"
	agent.Mounts = []inventory.Mount{{Type: "bind", Source: "/srv"}}
	inv.Containers = append(inv.Containers, agent)
	p := preview(t, other, inv, nil, "")
	want := []string{"container_name:shop-web-1", "port:8080/tcp", "network:shop_default", "dependency:proxy", "volume:shop_data", "bind_path:/srv/shop/conf"}
	got := kinds(p)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing collision %s in %s", w, got)
		}
	}
	if !p.Blocked || p.Production || p.Mode != "alternate_host" {
		t.Fatalf("blocked=%v production=%v mode=%s", p.Blocked, p.Production, p.Mode)
	}
}

func TestCleanAlternateHostWithRemap(t *testing.T) {
	inv := &inventory.Inventory{Networks: []inventory.Network{{Name: "proxy"}}}
	p := preview(t, other, inv, []PathRemap{{From: "/srv/shop", To: "/srv/shop-restored"}}, "")
	if p.Blocked || p.Production || len(p.CreateContainers) != 2 || p.Components[2].Target != "/srv/shop-restored/conf" {
		t.Fatalf("preview %+v collisions %s", p, kinds(p))
	}
	if p.Components[0].Target != "/srv/shop-restored" || p.Components[1].Action != "create" {
		t.Fatalf("components %+v", p.Components)
	}
	if p.Networks[0].Action != "create" || p.Networks[1].Action != "exists" {
		t.Fatalf("networks %+v", p.Networks)
	}
}

func TestProductionTagAndUnknownInventory(t *testing.T) {
	if p := preview(t, other, &inventory.Inventory{Networks: []inventory.Network{{Name: "proxy"}}}, nil, "production"); !p.Production {
		t.Fatal("production tag ignored")
	}
	if p := preview(t, other, nil, nil, ""); !p.Blocked {
		t.Fatal("restore without target inventory must be blocked")
	}
}

func TestSelectAndRemap(t *testing.T) {
	m := testManifest()
	if _, err := selectComponents(m, []string{"fsmeta:volume:shop_data"}); err == nil {
		t.Fatal("fsmeta selected on its own")
	}
	if _, err := selectComponents(m, []string{"image:web"}); err == nil {
		t.Fatal("failed component selected")
	}
	if _, err := selectComponents(m, []string{"nope"}); err == nil {
		t.Fatal("unknown component selected")
	}
	r := []PathRemap{{From: "/srv/shop/", To: "/data/shop"}}
	for in, want := range map[string]string{"/srv/shop": "/data/shop", "/srv/shop/a/b": "/data/shop/a/b", "/srv/shopping": "/srv/shopping"} {
		if got := Remap(in, r); got != want {
			t.Errorf("Remap(%s) = %s, want %s", in, got, want)
		}
	}
}
