// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	agentv1 "github.com/AxiomOperator/dbr2/internal/agentpb/agent/v1"
	"github.com/AxiomOperator/dbr2/internal/runtime"
)

const (
	maxInspectDoc        = 16 << 20
	defaultHealthTimeout = 5 * time.Minute
	healthLogLines       = 50
	// restartLoop is the RestartCount increase that fails CheckHealth early.
	restartLoop = 3
	// unhealthyGrace is how long Docker may report "unhealthy" (already
	// after the healthcheck's own retries) before CheckHealth fails early.
	unhealthyGrace = 30 * time.Second
)

// ---- EnsureImages -----------------------------------------------------------

// repoOf strips the tag and digest from an image reference
// ("registry:5000/app:1.2@sha256:…" → "registry:5000/app").
func repoOf(ref string) string {
	ref, _, _ = strings.Cut(ref, "@")
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	return ref
}

// normalizeRepo expands Docker Hub short names ("postgres" →
// "docker.io/library/postgres").
func normalizeRepo(repo string) string {
	first, rest, ok := strings.Cut(repo, "/")
	if !ok {
		return "docker.io/library/" + repo
	}
	if first == "index.docker.io" {
		first = "docker.io"
	}
	if first != "localhost" && !strings.ContainsAny(first, ".:") {
		return "docker.io/" + repo
	}
	if first == "docker.io" && !strings.Contains(rest, "/") {
		return "docker.io/library/" + rest
	}
	return first + "/" + rest
}

// hasDigest reports whether the image carries repo@digest.
func hasDigest(info runtime.ImageInfo, repo, digest string) bool {
	for _, rd := range info.RepoDigests {
		r, d, ok := strings.Cut(rd, "@")
		if ok && d == digest && normalizeRepo(r) == normalizeRepo(repo) {
			return true
		}
	}
	return false
}

// ensureImages makes every image present, pulling missing ones by digest
// and tagging them with their reference.
func (a *Agent) ensureImages(ctx context.Context, cmdID string, c *agentv1.EnsureImagesCommand) (*agentv1.EnsureImagesResult, error) {
	ctl, err := a.restoreControl()
	if err != nil {
		return nil, err
	}
	if !a.jobs.tryAcquire() {
		a.progress(cmdID, map[string]any{"queued": true})
		if err := a.jobs.acquire(ctx); err != nil {
			return nil, err
		}
	}
	defer a.jobs.release()
	res := &agentv1.EnsureImagesResult{}
	var failed []string
	for i, img := range c.Images {
		a.progress(cmdID, map[string]any{"done": i, "total": len(c.Images), "image": img.Ref})
		r := &agentv1.ImageResult{Ref: img.Ref, Digest: img.Digest}
		status, err := a.ensureImage(ctx, ctl, img)
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			r.Status, r.Error = "failed", err.Error()
			failed = append(failed, img.Ref)
			a.log.Warn("image not available", "ref", img.Ref, "digest", img.Digest, "err", err)
		} else {
			r.Status = status
			a.log.Info("image ensured", "ref", img.Ref, "digest", img.Digest, "status", status)
		}
		res.Images = append(res.Images, r)
	}
	if len(failed) > 0 {
		return res, fmt.Errorf("images not available: %s", strings.Join(failed, ", "))
	}
	return res, nil
}

func (a *Agent) ensureImage(ctx context.Context, ctl runtime.RestoreControl, img *agentv1.ImageSpec) (string, error) {
	if img.Ref == "" {
		return "", errors.New("empty image reference")
	}
	if img.Digest == "" {
		if _, err := ctl.InspectImage(ctx, img.Ref); err == nil {
			return "present", nil
		} else if !errors.Is(err, runtime.ErrNotFound) {
			return "", err
		}
		if err := ctl.PullImage(ctx, img.Ref); err != nil {
			return "", fmt.Errorf("pull %s: %w", img.Ref, err)
		}
		return "pulled", nil
	}
	repo := repoOf(img.Ref)
	pinned := repo + "@" + img.Digest
	status := "present"
	info, err := ctl.InspectImage(ctx, pinned)
	if err != nil && !errors.Is(err, runtime.ErrNotFound) {
		return "", err
	}
	if err != nil || !hasDigest(info, repo, img.Digest) {
		if err := ctl.PullImage(ctx, pinned); err != nil {
			return "", fmt.Errorf("pull %s: %w", pinned, err)
		}
		if info, err = ctl.InspectImage(ctx, pinned); err != nil {
			return "", fmt.Errorf("inspect pulled %s: %w", pinned, err)
		}
		if !hasDigest(info, repo, img.Digest) {
			return "", fmt.Errorf("pulled image %s does not carry digest %s", info.ID, img.Digest)
		}
		status = "pulled"
	}
	if strings.Contains(img.Ref, "@") {
		return status, nil // the reference is digest-pinned itself
	}
	// Point ref at the captured image so recreated containers run it.
	if cur, err := ctl.InspectImage(ctx, img.Ref); err == nil && cur.ID == info.ID {
		return status, nil
	}
	if err := ctl.TagImage(ctx, info.ID, img.Ref); err != nil {
		return "", fmt.Errorf("tag %s: %w", img.Ref, err)
	}
	return status, nil
}

// ---- RecreateContainers -----------------------------------------------------

var builtinNetworks = map[string]bool{"bridge": true, "host": true, "none": true, "default": true}

// recreateContainers creates missing networks, then the containers of the
// config snapshot that do not exist on the host (not started).
func (a *Agent) recreateContainers(ctx context.Context, c *agentv1.RecreateContainersCommand) (*agentv1.RecreateContainersResult, error) {
	if err := validRestoreID(c.RestoreId); err != nil {
		return nil, err
	}
	if c.ConfigSnapshotId == "" {
		return nil, permanent(errors.New("config_snapshot_id is required"))
	}
	if _, err := a.loadConnection(c.RepositoryId); err != nil {
		return nil, err
	}
	ctl, err := a.restoreControl()
	if err != nil {
		return nil, err
	}
	unlock := a.restores.lock(c.RestoreId)
	defer unlock()
	j, err := a.restores.open(c.RestoreId)
	if err != nil {
		return nil, err
	}
	repo, release, err := a.repoSession(ctx, c.RepositoryId, c.FreshSession)
	if err != nil {
		return nil, err
	}
	defer release()

	// Read and convert every inspect document first: nothing is created
	// when one is unusable.
	ents, err := repo.ListDir(ctx, c.ConfigSnapshotId, "containers")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, engineErr(fmt.Errorf("config snapshot %s: %w", c.ConfigSnapshotId, err))
	}
	remap := remapper(c.PathRemaps)
	var specs []runtime.ContainerSpec
	for _, e := range ents {
		if !e.IsRegular() || !strings.HasSuffix(e.Name, ".json") || e.Name == "errors.json" {
			continue
		}
		rc, err := repo.OpenFile(ctx, c.ConfigSnapshotId, "containers/"+e.Name)
		if err != nil {
			return nil, engineErr(err)
		}
		raw, err := io.ReadAll(io.LimitReader(rc, maxInspectDoc))
		rc.Close()
		if err != nil {
			return nil, engineErr(err)
		}
		spec, err := runtime.ContainerSpecFromInspect(raw, remap)
		if err != nil {
			return nil, permanent(fmt.Errorf("containers/%s: %w", e.Name, err))
		}
		specs = append(specs, spec)
	}
	sort.Slice(specs, func(i, k int) bool { return specs[i].Name < specs[k].Name })

	res := &agentv1.RecreateContainersResult{}
	if err := a.ensureNetworks(ctx, ctl, j, c.Networks, res); err != nil {
		return res, err
	}
	var failed []string
	for _, s := range specs {
		rc := &agentv1.RecreatedContainer{Name: s.Name, WasRunning: s.WasRunning}
		res.Containers = append(res.Containers, rc)
		d, err := ctl.InspectDetails(ctx, s.Name)
		if err == nil {
			rc.Status, rc.ContainerId = "existing", d.ID
			continue
		}
		if !errors.Is(err, runtime.ErrNotFound) {
			return res, fmt.Errorf("inspect %s: %w", s.Name, err)
		}
		j.CreatedContainers = append(j.CreatedContainers, createdContainer{Name: s.Name})
		if err := a.restores.save(j); err != nil {
			return res, err
		}
		id, err := ctl.CreateContainer(ctx, s)
		if err != nil {
			j.CreatedContainers = j.CreatedContainers[:len(j.CreatedContainers)-1]
			rc.Status, rc.Error = "failed", err.Error()
			failed = append(failed, s.Name)
			a.log.Warn("container create failed", "restore_id", c.RestoreId, "container", s.Name, "err", err)
			continue
		}
		j.CreatedContainers[len(j.CreatedContainers)-1].ID = id
		rc.Status, rc.ContainerId = "created", id
		a.log.Info("container created", "restore_id", c.RestoreId, "container", s.Name, "id", id)
	}
	if err := a.restores.save(j); err != nil {
		return res, err
	}
	if len(failed) > 0 {
		return res, permanent(fmt.Errorf("containers not created: %s", strings.Join(failed, ", ")))
	}
	return res, nil
}

func (a *Agent) ensureNetworks(ctx context.Context, ctl runtime.RestoreControl, j *restoreJournal, nets []*agentv1.NetworkSpec,
	res *agentv1.RecreateContainersResult) error {
	for _, n := range nets {
		if n.Name == "" || builtinNetworks[n.Name] {
			continue
		}
		ok, err := ctl.NetworkExists(ctx, n.Name)
		if err != nil {
			return fmt.Errorf("inspect network %s: %w", n.Name, err)
		}
		if ok {
			continue
		}
		if n.External {
			return permanent(fmt.Errorf("external network %s does not exist; create it before restoring", n.Name))
		}
		j.CreatedNetworks = append(j.CreatedNetworks, n.Name)
		if err := a.restores.save(j); err != nil {
			return err
		}
		if _, err := ctl.CreateNetwork(ctx, runtime.NetworkSpec{Name: n.Name, Driver: n.Driver, Labels: n.Labels,
			Internal: n.Internal, Attachable: n.Attachable}); err != nil {
			j.CreatedNetworks = j.CreatedNetworks[:len(j.CreatedNetworks)-1]
			_ = a.restores.save(j)
			return fmt.Errorf("create network %s: %w", n.Name, err)
		}
		res.CreatedNetworks = append(res.CreatedNetworks, n.Name)
		a.log.Info("network created", "restore_id", j.RestoreID, "network", n.Name)
	}
	return nil
}

// ---- StartContainers --------------------------------------------------------

// startContainers starts (or unpauses) every container that is not
// running; started lists the containers this call changed.
func (a *Agent) startContainers(ctx context.Context, c *agentv1.StartContainersCommand) (*agentv1.StartContainersResult, error) {
	ctl, err := a.control()
	if err != nil {
		return nil, err
	}
	res := &agentv1.StartContainersResult{Started: []string{}}
	for _, id := range c.ContainerIds {
		s, err := ctl.InspectState(ctx, id)
		if err != nil {
			return res, fmt.Errorf("inspect %s: %w", id, err)
		}
		switch s.State {
		case "running", "restarting":
			continue
		case "paused":
			err = ctl.Unpause(ctx, id)
		default:
			err = ctl.Start(ctx, id)
		}
		if err != nil {
			return res, permanent(fmt.Errorf("start %s: %w", s.Name, err))
		}
		res.Started = append(res.Started, id)
	}
	a.log.Info("containers started", "restore_id", c.RestoreId, "started", len(res.Started), "requested", len(c.ContainerIds))
	return res, nil
}

// ---- CheckHealth ------------------------------------------------------------

// checkHealth polls until every container is running (and healthy when it
// has a healthcheck) continuously for stable_seconds. It fails early when
// a container exits, disappears or restarts repeatedly, and on timeout.
func (a *Agent) checkHealth(ctx context.Context, c *agentv1.CheckHealthCommand) (*agentv1.CheckHealthResult, error) {
	ctl, err := a.restoreControl()
	if err != nil {
		return nil, err
	}
	if len(c.ContainerIds) == 0 {
		return &agentv1.CheckHealthResult{Ok: true}, nil
	}
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = defaultHealthTimeout
	}
	stable := time.Duration(c.StableSeconds) * time.Second
	poll := a.healthPoll
	if poll == 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(timeout)
	baseRestarts := map[string]int{}
	unhealthySince := map[string]time.Time{}
	var okSince time.Time
	for {
		res := &agentv1.CheckHealthResult{Ok: true}
		var fatal []string
		for _, id := range c.ContainerIds {
			h := &agentv1.ContainerHealth{ContainerId: id}
			res.Containers = append(res.Containers, h)
			d, err := ctl.InspectDetails(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				res.Ok = false
				h.State = "missing"
				if errors.Is(err, runtime.ErrNotFound) {
					fatal = append(fatal, id+" no longer exists")
				}
				continue
			}
			h.Name, h.State, h.Health, h.ExitCode = d.Name, d.State, d.Health, int32(d.ExitCode) //nolint:gosec // exit codes are small
			h.Ok = d.State == "running" && (d.Health == "none" || d.Health == "healthy")
			res.Ok = res.Ok && h.Ok
			base, seen := baseRestarts[id]
			if !seen {
				baseRestarts[id] = d.RestartCount
			}
			if d.Health == "unhealthy" {
				if unhealthySince[id].IsZero() {
					unhealthySince[id] = time.Now()
				}
			} else {
				delete(unhealthySince, id)
			}
			switch {
			case !unhealthySince[id].IsZero() && time.Since(unhealthySince[id]) >= a.unhealthyAfter():
				fatal = append(fatal, fmt.Sprintf("%s unhealthy for %s", d.Name, time.Since(unhealthySince[id]).Round(time.Second)))
			case d.State == "exited" || d.State == "dead":
				fatal = append(fatal, fmt.Sprintf("%s %s (exit code %d)", d.Name, d.State, d.ExitCode))
			case seen && d.RestartCount-base >= restartLoop:
				fatal = append(fatal, fmt.Sprintf("%s restarted %d times", d.Name, d.RestartCount-base))
			}
		}
		now := time.Now()
		var failure error
		switch {
		case len(fatal) > 0:
			res.Ok, failure = false, fmt.Errorf("application unhealthy: %s", strings.Join(fatal, "; "))
		case res.Ok:
			if okSince.IsZero() {
				okSince = now
			}
			if now.Sub(okSince) >= stable {
				a.log.Info("application healthy", "containers", len(c.ContainerIds), "stable_for", now.Sub(okSince).Round(time.Second))
				return res, nil
			}
		default:
			okSince = time.Time{}
		}
		if failure == nil && now.After(deadline) {
			res.Ok, failure = false, fmt.Errorf("application not healthy within %s", timeout)
		}
		if failure != nil {
			a.healthLogs(ctx, ctl, res)
			a.log.Warn("health check failed", "err", failure)
			return res, permanent(failure)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (a *Agent) unhealthyAfter() time.Duration {
	if a.unhealthyGrace > 0 {
		return a.unhealthyGrace
	}
	return unhealthyGrace
}

// healthLogs attaches the last log lines of every container that is not ok.
func (a *Agent) healthLogs(ctx context.Context, ctl runtime.RestoreControl, res *agentv1.CheckHealthResult) {
	for _, h := range res.Containers {
		if h.Ok || h.State == "missing" {
			continue
		}
		logs, err := ctl.Logs(ctx, h.ContainerId, healthLogLines)
		if err != nil {
			logs = "[logs unavailable: " + err.Error() + "]"
		}
		h.LogTail = strings.ToValidUTF8(logs, "�")
	}
}
