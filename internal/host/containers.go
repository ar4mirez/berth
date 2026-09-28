package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// Container engines (#57). berth drives an engine through its CLI, with Docker's argv; the engine
// of a host decides which program runs it. Docker is the default and is untouched.
const (
	EngineDocker = "docker"
	EnginePodman = "podman"
)

// Engines are the engines berth can drive.
var Engines = []string{EngineDocker, EnginePodman}

// CheckEngine refuses an engine berth doesn't know ("" is Docker).
func CheckEngine(e string) error {
	if e == "" || slices.Contains(Engines, e) {
		return nil
	}
	return fmt.Errorf("unknown container engine %q (docker or podman)", e)
}

// EngineName is h's engine: docker unless WithEngine said otherwise.
func (h *Host) EngineName() string {
	if h.engine == "" {
		return EngineDocker
	}
	return h.engine
}

// WithEngine is h driving engine: every command whose program is "docker" runs engine's CLI instead
// (Podman takes Docker's argv, compose included), and the published ports come from that CLI. For
// Docker (or ""), h itself.
func WithEngine(h *Host, engine string) *Host {
	if engine == "" || engine == EngineDocker {
		return h
	}
	ex := engineExec{inner: h.Exec, bin: engine}
	g := *h
	g.Exec = ex
	g.Facts = cliFacts{ExecFacts: ExecFacts{Exec: ex}, ex: ex}
	g.engine = engine
	return &g
}

type engineExec struct {
	inner Execer
	bin   string
}

func (e engineExec) Run(ctx context.Context, c Cmd) error {
	if len(c.Args) > 0 && c.Args[0] == EngineDocker {
		c.Args = append([]string{e.bin}, c.Args[1:]...)
	}
	return e.inner.Run(ctx, c)
}

// cliFacts reads the ports published by containers from the engine's CLI (`ps --format json`),
// rather than Docker's API: a rootful Podman publishes them with firewall rules, not listening
// sockets, so ss alone would miss them.
type cliFacts struct {
	ExecFacts
	ex Execer
}

func (f cliFacts) PortsInUse(ctx context.Context) ([]int, error) {
	ports, err := f.listening(ctx)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := f.ex.Run(ctx, Cmd{Args: []string{EngineDocker, "ps", "--format", "json"}, Stdout: &out}); err != nil {
		return nil, fmt.Errorf("listing the engine's containers: %w", err)
	}
	pub, err := parsePodmanPorts(out.Bytes())
	if err != nil {
		return nil, err
	}
	ports = append(ports, pub...)
	slices.Sort(ports)
	return slices.Compact(ports), nil
}

// parsePodmanPorts reads `podman ps --format json`: an array of containers with
// Ports: [{host_port, range, …}].
func parsePodmanPorts(b []byte) ([]int, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var cs []struct {
		Ports []struct {
			HostPort int `json:"host_port"`
			Range    int `json:"range"`
		}
	}
	if err := json.Unmarshal(b, &cs); err != nil {
		return nil, fmt.Errorf("the engine's container list: %w", err)
	}
	var ports []int
	for _, c := range cs {
		for _, p := range c.Ports {
			n := max(p.Range, 1)
			for i := 0; i < n && p.HostPort > 0; i++ {
				ports = append(ports, p.HostPort+i)
			}
		}
	}
	return ports, nil
}
