package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PrereqError says what is missing and how to fix it. ZAKS Drive never installs Docker or
// changes system permissions itself.
type PrereqError struct {
	Kind string // missing, permission, daemon, compose, other
	Msg  string
	Fix  string
}

func (e *PrereqError) Error() string { return e.Msg }

// Prereq is the detected Docker environment.
type Prereq struct {
	DockerServer string // Docker Engine version
	Compose      string // Docker Compose version
}

// checkDocker verifies that the Docker CLI exists, that the daemon is reachable by this user,
// and that the Compose v2 plugin is installed.
func (a *App) checkDocker(ctx context.Context) (Prereq, error) {
	var p Prereq
	res, err := a.Runner.Run(ctx, "docker", []string{"version", "--format", "{{.Server.Version}}"}, RunOpts{})
	if errors.Is(err, ErrNotInstalled) {
		return p, &PrereqError{
			Kind: "missing",
			Msg:  "Docker is not installed (the docker command was not found on PATH)",
			Fix:  "Install Docker Engine and the Compose plugin: https://docs.docker.com/engine/install/ . zaks-drive does not install Docker for you.",
		}
	}
	if err != nil {
		return p, &PrereqError{Kind: "other", Msg: "could not run docker: " + oneLine(err.Error()), Fix: "Check that the docker command works for your user: docker version"}
	}
	if res.ExitCode != 0 {
		msg := strings.ToLower(res.Stderr)
		switch {
		case strings.Contains(msg, "permission denied"):
			return p, &PrereqError{
				Kind: "permission",
				Msg:  "your user may not use the Docker daemon (permission denied on the Docker socket)",
				Fix:  "Run zaks-drive as a user in the \"docker\" group (sudo usermod -aG docker $USER, then log in again), or use sudo. zaks-drive never changes group membership for you.",
			}
		case strings.Contains(msg, "cannot connect") || strings.Contains(msg, "is the docker daemon running"):
			return p, &PrereqError{Kind: "daemon", Msg: "the Docker daemon is not running or not reachable", Fix: "Start it: sudo systemctl start docker"}
		}
		return p, &PrereqError{Kind: "other", Msg: "docker version failed: " + oneLine(trimLines(res.Stderr, 2)), Fix: "Run docker version and fix what it reports."}
	}
	p.DockerServer = strings.TrimSpace(res.Stdout)

	res, err = a.Runner.Run(ctx, "docker", []string{"compose", "version", "--short"}, RunOpts{})
	if err != nil || res.ExitCode != 0 {
		return p, &PrereqError{
			Kind: "compose",
			Msg:  "Docker Compose v2 is not available (docker compose version failed)",
			Fix:  "Install the Compose plugin (docker-compose-plugin package, see https://docs.docker.com/compose/install/linux/). The old docker-compose v1 command is not supported.",
		}
	}
	p.Compose = strings.TrimSpace(res.Stdout)
	return p, nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// composeArgs builds the arguments of a compose command for this deployment only: the compose
// file is always given explicitly, so nothing outside the deployment can be affected.
func (d *Deployment) composeArgs(sub ...string) []string {
	return append([]string{"compose", "-f", d.ComposeFile}, sub...)
}

// composeEnv returns the environment additions for compose. The version recorded by setup or
// update selects the image tag; a value set by the user in the process environment wins.
func (a *App) composeEnv(d *Deployment) []string {
	if a.env("ZAKS_DRIVE_VERSION") != "" {
		return nil
	}
	if s := d.loadState(); s.Current != "" {
		return []string{"ZAKS_DRIVE_VERSION=" + s.Current}
	}
	return nil
}

func (a *App) compose(ctx context.Context, d *Deployment, opts RunOpts, sub ...string) (Result, error) {
	opts.Dir = d.Dir
	opts.Env = append(opts.Env, a.composeEnv(d)...)
	return a.Runner.Run(ctx, "docker", d.composeArgs(sub...), opts)
}

// Container is one container of the deployment, as reported by docker compose ps.
type Container struct {
	ID           string
	Name         string
	Service      string
	State        string // running, exited, restarting, created, ...
	Health       string // healthy, unhealthy, starting, or empty
	ExitCode     int
	Image        string
	Ports        []string // "127.0.0.1:9090->9090/tcp"
	RestartCount int
}

type composePS struct {
	ID         string `json:"ID"`
	Name       string `json:"Name"`
	Service    string `json:"Service"`
	State      string `json:"State"`
	Health     string `json:"Health"`
	ExitCode   int    `json:"ExitCode"`
	Image      string `json:"Image"`
	Publishers []struct {
		URL           string `json:"URL"`
		TargetPort    int    `json:"TargetPort"`
		PublishedPort int    `json:"PublishedPort"`
		Protocol      string `json:"Protocol"`
	} `json:"Publishers"`
}

// parseComposePS accepts both output shapes of docker compose ps --format json: one JSON object
// per line (older Compose v2) or a single JSON array (newer).
func parseComposePS(out string) ([]Container, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var items []composePS
	if strings.HasPrefix(out, "[") {
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			return nil, fmt.Errorf("cannot read docker compose ps output: %w", err)
		}
	} else {
		dec := json.NewDecoder(bytes.NewReader([]byte(out)))
		for dec.More() {
			var it composePS
			if err := dec.Decode(&it); err != nil {
				return nil, fmt.Errorf("cannot read docker compose ps output: %w", err)
			}
			items = append(items, it)
		}
	}
	var cs []Container
	for _, it := range items {
		c := Container{ID: it.ID, Name: it.Name, Service: it.Service, State: it.State, Health: it.Health, ExitCode: it.ExitCode, Image: it.Image}
		for _, p := range it.Publishers {
			if p.PublishedPort == 0 {
				continue
			}
			host := p.URL
			if host == "" {
				host = "0.0.0.0"
			}
			c.Ports = append(c.Ports, fmt.Sprintf("%s:%d->%d/%s", host, p.PublishedPort, p.TargetPort, p.Protocol))
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// managed lists the containers of this deployment (all states). Only containers that docker
// compose reports for the deployment's compose file are returned, so unrelated containers are
// never touched.
func (a *App) managed(ctx context.Context, d *Deployment) ([]Container, error) {
	res, err := a.compose(ctx, d, RunOpts{}, "ps", "--all", "--format", "json")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("docker compose ps failed: %s", oneLine(trimLines(res.Stderr, 3)))
	}
	cs, err := parseComposePS(res.Stdout)
	if err != nil {
		return nil, err
	}
	// Restart counts come from docker inspect, restricted to the IDs compose just returned.
	if len(cs) > 0 {
		args := []string{"inspect", "--format", "{{.Id}} {{.RestartCount}}"}
		for _, c := range cs {
			if c.ID != "" {
				args = append(args, c.ID)
			}
		}
		if ir, err := a.Runner.Run(ctx, "docker", args, RunOpts{}); err == nil && ir.ExitCode == 0 {
			counts := map[string]int{}
			for _, line := range strings.Split(ir.Stdout, "\n") {
				f := strings.Fields(line)
				if len(f) == 2 {
					if n, err := strconv.Atoi(f[1]); err == nil {
						counts[f[0]] = n
					}
				}
			}
			for i := range cs {
				for id, n := range counts {
					if strings.HasPrefix(id, cs[i].ID) || strings.HasPrefix(cs[i].ID, id) {
						cs[i].RestartCount = n
					}
				}
			}
		}
	}
	return cs, nil
}

// imageExists reports whether the image tag exists locally.
func (a *App) imageExists(ctx context.Context, ref string) bool {
	res, err := a.Runner.Run(ctx, "docker", []string{"image", "inspect", "--format", "{{.Id}}", ref}, RunOpts{})
	return err == nil && res.ExitCode == 0
}
