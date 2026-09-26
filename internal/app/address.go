package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/hosts"
)

// org@host addressing (#45): `berth up acme@box1` runs `up` against the registered host box1, with
// its state root; a bare `acme` (or acme@local) stays on this machine, exactly as before.

// SplitAddress splits an org argument at its last "@": ("acme", "box1") for acme@box1, and
// (arg, "") when there's no host part.
func SplitAddress(arg string) (org, hostName string) {
	i := strings.LastIndex(arg, "@")
	if i < 0 {
		return arg, ""
	}
	return arg[:i], arg[i+1:]
}

var addressLike = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*@[a-z0-9][a-z0-9-]*$`)

// RefuseRemote fails when one of args is an org@host address for another host: for commands that
// can't act on another host yet.
func RefuseRemote(cmd string, args ...string) error {
	for _, x := range args {
		if !addressLike.MatchString(x) {
			continue
		}
		if _, h := SplitAddress(x); h != hosts.Local {
			return fmt.Errorf("%s can't act on an org on another host yet (%s); run it on that host, or see docs/hosts.md", cmd, x)
		}
	}
	return nil
}

// On is a for another host: h's files, processes and Docker, with home as the state root. The
// operator's machine, stdio, environment and output format stay.
func (a *App) On(name string, h *host.Host, home string) *App {
	st := a.State
	st.Home = config.Home{Path: home, Source: config.SourceHost}
	b := New(st, h, a.Stdin, a.Stdout, a.Stderr, a.Getenv)
	b.Operator, b.HostName = a.Operator, name
	b.OpenTTY, b.Output, b.Self, b.Invoked = a.OpenTTY, a.Output, a.Self, a.Invoked
	return b
}

// At resolves an org argument. A bare org, or org@local, is a itself; org@<host> connects to that
// registered host and returns an App for it (done closes the connection). org is the bare name.
func (a *App) At(ctx context.Context, arg string) (b *App, org string, done func(), err error) {
	org, name := SplitAddress(arg)
	if name == "" || name == hosts.Local {
		return a, org, func() {}, nil
	}
	if a.HostName != "" {
		return nil, "", nil, errors.New("already on a host")
	}
	e, err := a.registered(name)
	if err != nil {
		return nil, "", nil, err
	}
	h, err := a.dialHost(ctx, e, e.Key)
	if err != nil {
		return nil, "", nil, fmt.Errorf("host %s: %w", name, err)
	}
	return a.On(name, h, e.Home), org, func() { _ = h.Close() }, nil
}

// hostLabel names the host a's orgs are on: "local" or the registered name.
func (a *App) hostLabel() string {
	if a.HostName == "" {
		return hosts.Local
	}
	return a.HostName
}

// registered is the registry entry for name.
func (a *App) registered(name string) (hosts.Entry, error) {
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		return hosts.Entry{}, err
	}
	e, ok := reg.Find(name)
	if !ok {
		return hosts.Entry{}, fmt.Errorf("unknown host '%s' (see: %s host ls)", name, Tool)
	}
	return e, nil
}

// HostNames are the registered hosts (for completion). Nothing when the registry can't be read.
func (a *App) HostNames() []string {
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range reg.Hosts {
		names = append(names, e.Name)
	}
	return names
}

// otherHosts are the registered hosts, for the views that span them (ls). A registry that can't
// be read is reported on stderr and treated as empty, so this machine's orgs still show.
func (a *App) otherHosts() []hosts.Entry {
	if a.HostName != "" {
		return nil
	}
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(a.Stderr, "%s: %v\n", Tool, err)
		}
		return nil
	}
	return reg.Hosts
}
