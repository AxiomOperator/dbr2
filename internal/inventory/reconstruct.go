// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Reconstruct generates a Compose file for an application from Docker
// runtime metadata (roadmap Phase 3: flagged Reconstructed). It never claims
// to equal the original: a header says so, and sensitive environment values
// become ${KEY} placeholders to be supplied at restore time.
func Reconstruct(a *Application, inv *Inventory) (string, error) {
	byName := map[string]Container{}
	for _, c := range inv.Containers {
		byName[c.Name] = c
	}
	vols := map[string]Volume{}
	for _, v := range inv.Volumes {
		vols[v.Name] = v
	}

	root := mapping()
	services := mapping()
	topVolumes := mapping()
	topNetworks := mapping()
	var placeholders []string

	for _, s := range a.Services {
		if len(s.Containers) == 0 {
			continue
		}
		c := byName[s.Containers[0].Name]
		svc := mapping()
		put(svc, "image", scalar(c.Image))
		if a.Kind != KindCompose {
			put(svc, "container_name", scalar(c.Name))
		}
		if len(s.Containers) > 1 {
			put(svc, "deploy", mappingOf("replicas", scalar(fmt.Sprint(len(s.Containers)))))
		}
		if c.RestartPolicy != "" && c.RestartPolicy != "no" {
			put(svc, "restart", scalar(c.RestartPolicy))
		}
		if len(c.Entrypoint) > 0 {
			put(svc, "entrypoint", seq(c.Entrypoint))
		}
		if len(c.Cmd) > 0 {
			put(svc, "command", seq(c.Cmd))
		}
		if c.User != "" {
			put(svc, "user", scalar(c.User))
		}
		if c.WorkingDir != "" {
			put(svc, "working_dir", scalar(c.WorkingDir))
		}
		if len(c.Env) > 0 {
			env := mapping()
			for _, e := range c.Env {
				if isImageDefaultEnv(e.Key) {
					continue
				}
				if e.Sensitive {
					put(env, e.Key, scalar("${"+e.Key+"}"))
					placeholders = append(placeholders, e.Key)
				} else {
					put(env, e.Key, scalar(e.Value))
				}
			}
			if len(env.Content) > 0 {
				put(svc, "environment", env)
			}
		}
		if len(c.Ports) > 0 {
			var ports []string
			for _, p := range c.Ports {
				if p.HostPort == "" {
					continue
				}
				s := p.HostPort + ":" + p.ContainerPort
				if p.HostIP != "" && p.HostIP != "0.0.0.0" && p.HostIP != "::" {
					s = p.HostIP + ":" + s
				}
				if p.Protocol != "" && p.Protocol != "tcp" {
					s += "/" + p.Protocol
				}
				ports = append(ports, s)
			}
			sort.Strings(ports)
			if len(ports) > 0 {
				put(svc, "ports", seq(dedupe(ports)))
			}
		}
		var mounts []string
		for _, m := range c.Mounts {
			ro := ""
			if !m.RW {
				ro = ":ro"
			}
			switch m.Type {
			case MountVolume:
				if isAnonymous(m.Name) {
					mounts = append(mounts, m.Destination)
					continue
				}
				short := composeVolumeName(m.Name, a.ComposeProject)
				mounts = append(mounts, short+":"+m.Destination+ro)
				vol := mapping()
				if short != m.Name || vols[m.Name].Labels[LabelProject] != a.ComposeProject || a.Kind != KindCompose {
					put(vol, "name", scalar(m.Name))
				}
				if a.Kind == KindCompose && vols[m.Name].Labels[LabelProject] != a.ComposeProject {
					put(vol, "external", scalar("true"))
				}
				if d := vols[m.Name].Driver; d != "" && d != "local" {
					put(vol, "driver", scalar(d))
				}
				putOnce(topVolumes, short, vol)
			case MountBind:
				mounts = append(mounts, m.Source+":"+m.Destination+ro)
			}
		}
		if len(mounts) > 0 {
			put(svc, "volumes", seq(mounts))
		}
		if len(c.Tmpfs) > 0 {
			var t []string
			for d := range c.Tmpfs {
				t = append(t, d)
			}
			sort.Strings(t)
			put(svc, "tmpfs", seq(t))
		}
		if strings.HasPrefix(c.NetworkMode, "container:") || c.NetworkMode == "host" {
			put(svc, "network_mode", scalar(c.NetworkMode))
		} else {
			var nets []string
			for _, n := range c.Networks {
				if isBuiltinNetwork(n) {
					continue
				}
				short := composeNetworkName(n, a.ComposeProject)
				nets = append(nets, short)
				net := mapping()
				external := true
				for _, nu := range a.Networks {
					if nu.Name == n {
						external = nu.External || a.Kind != KindCompose
					}
				}
				if external {
					put(net, "name", scalar(n))
					put(net, "external", scalar("true"))
				} else if short != n {
					put(net, "name", scalar(n))
				}
				putOnce(topNetworks, short, net)
			}
			sort.Strings(nets)
			if len(nets) > 0 {
				put(svc, "networks", seq(dedupe(nets)))
			}
		}
		labels := mapping()
		keys := make([]string, 0, len(c.Labels))
		for k := range c.Labels {
			if !strings.HasPrefix(k, "com.docker.compose.") && !strings.HasPrefix(k, "org.opencontainers.") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			put(labels, k, scalar(c.Labels[k]))
		}
		if len(labels.Content) > 0 {
			put(svc, "labels", labels)
		}
		put(services, s.Name, svc)
	}
	put(root, "services", services)
	if len(topVolumes.Content) > 0 {
		put(root, "volumes", topVolumes)
	}
	if len(topNetworks.Content) > 0 {
		put(root, "networks", topNetworks)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# RECONSTRUCTED by DBR² from Docker runtime metadata (%s).\n", inv.CollectedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "# This is NOT the original Compose file: review it before use.\n")
	if a.SourceReason != "" {
		fmt.Fprintf(&b, "# Reason: %s\n", a.SourceReason)
	}
	if len(placeholders) > 0 {
		sort.Strings(placeholders)
		fmt.Fprintf(&b, "# Sensitive values were replaced by placeholders; supply them at restore time: %s\n", strings.Join(dedupe(placeholders), ", "))
	}
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return "", err
	}
	return b.String(), enc.Close()
}

// composeVolumeName strips the "<project>_" prefix Compose adds.
func composeVolumeName(name, project string) string {
	if project != "" && strings.HasPrefix(name, project+"_") {
		return strings.TrimPrefix(name, project+"_")
	}
	return name
}

func composeNetworkName(name, project string) string { return composeVolumeName(name, project) }

// Variables every image sets; not part of the application's configuration.
func isImageDefaultEnv(k string) bool {
	switch k {
	case "PATH", "HOME", "HOSTNAME", "container":
		return true
	}
	return false
}

func dedupe(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func mapping() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode} }

func mappingOf(k string, v *yaml.Node) *yaml.Node {
	m := mapping()
	put(m, k, v)
	return m
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v} }

func seq(vs []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range vs {
		n.Content = append(n.Content, scalar(v))
	}
	return n
}

func put(m *yaml.Node, k string, v *yaml.Node) {
	m.Content = append(m.Content, scalar(k), v)
}

func putOnce(m *yaml.Node, k string, v *yaml.Node) {
	for i := 0; i < len(m.Content); i += 2 {
		if m.Content[i].Value == k {
			return
		}
	}
	if len(v.Content) == 0 {
		v = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: ""}
	}
	put(m, k, v)
}
