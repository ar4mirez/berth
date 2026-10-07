// Package tui is `berth tui`: an interactive dashboard for hosts and orgs (#59).
//
// Every view reads through the operations layer (internal/ops), and every action is an operation
// of its catalog, which decides what the dashboard asks before running it: an operation that
// restarts a container shows what stops and must be confirmed; under --read-only no write runs.
package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
)

// Action is something the dashboard does to an org: an operation of ops.Catalog (Cmd, Sub) and its
// arguments.
type Action struct {
	Cmd, Sub string
	// Org is the org's address: acme, or acme@box1.
	Org  string
	Args []string
}

// Label is the action as a person would type it.
func (a Action) Label() string {
	return ops.Respell(strings.Join(append(strings.Fields("berth "+a.Cmd+" "+a.Sub), append([]string{a.Org}, a.Args...)...), " "))
}

// Detail is what an org's page shows. Each part is read on its own: one that fails says so in its
// tab, and the others still show.
type Detail struct {
	Org      string
	Info     ops.Info
	Firewall ops.Firewall
	Repos    ops.Repos
	Env      ops.Env
	Packages ops.Packages
	Backups  ops.Backups
	// Errs is each part's error, by tab name.
	Errs map[string]error
}

// Backend is what the dashboard reads and does. The real one is the operations layer on an App;
// the tests use a fixed one.
type Backend interface {
	ReadOnly() bool
	Orgs(ctx context.Context) ops.Orgs
	Hosts(ctx context.Context) (ops.Hosts, error)
	Detail(ctx context.Context, org string) Detail
	Logs(ctx context.Context, org string, lines int) (ops.Logs, error)
	// Run does an action and returns what berth printed doing it.
	Run(ctx context.Context, a Action) (string, error)
	// Handoff takes the terminal over for an org ("attach" or "shell") and returns when the
	// person leaves it.
	Handoff(ctx context.Context, org, kind string) error
}

// appBackend is the Backend on berth's own operations.
type appBackend struct {
	// newApp makes the App one call runs against, printing into the buffers given: the terminal
	// is the dashboard's.
	newApp   func(stdout, stderr io.Writer) *app.App
	readOnly bool
	// self and args start berth again for a handoff: this binary and its global flags.
	self string
	args []string
}

// NewBackend is the dashboard's backend: newApp as the CLI builds an App, self and global the
// binary and its global flags (--home …) for the commands that take the terminal over.
func NewBackend(newApp func(stdout, stderr io.Writer) *app.App, readOnly bool, self string, global []string) Backend {
	return &appBackend{newApp: newApp, readOnly: readOnly, self: self, args: global}
}

func (b *appBackend) ReadOnly() bool { return b.readOnly }

func (b *appBackend) app() *app.App { return b.newApp(io.Discard, io.Discard) }

func (b *appBackend) Orgs(ctx context.Context) ops.Orgs { return ops.ListOrgs(ctx, b.app()) }

func (b *appBackend) Hosts(ctx context.Context) (ops.Hosts, error) {
	return ops.GetHosts(ctx, b.app())
}

func (b *appBackend) Detail(ctx context.Context, org string) Detail {
	d := Detail{Org: org, Errs: map[string]error{}}
	a, o, done, err := b.app().At(ctx, org)
	if err != nil {
		for _, t := range tabs {
			d.Errs[t] = err
		}
		return d
	}
	defer done()
	d.Info, err = ops.GetInfo(ctx, a, o)
	d.Errs[tabInfo] = err
	d.Firewall, err = ops.GetFirewall(ctx, a, o)
	d.Errs[tabFirewall] = err
	d.Repos, err = ops.GetRepos(ctx, a, o)
	d.Errs[tabRepos] = err
	d.Env, err = ops.GetEnv(a, o)
	d.Errs[tabEnv] = err
	d.Packages, _ = ops.GetPackages(ctx, a, o)
	if a.HostName == "" { // backups are files on this machine
		d.Backups = ops.ListBackups(a, o)
	}
	return d
}

func (b *appBackend) Logs(ctx context.Context, org string, lines int) (ops.Logs, error) {
	a, o, done, err := b.app().At(ctx, org)
	if err != nil {
		return ops.Logs{}, err
	}
	defer done()
	return ops.TailLogs(ctx, a, o, lines)
}

func (b *appBackend) Run(ctx context.Context, act Action) (string, error) {
	var out, errb bytes.Buffer
	root := b.newApp(&out, &errb)
	a, o, done, err := root.At(ctx, act.Org)
	if err != nil {
		return "", err
	}
	defer done()
	switch act.Cmd + " " + act.Sub {
	case "up ":
		err = a.Up(ctx, o)
	case "down ":
		err = a.Down(ctx, o)
	case "restart ":
		err = a.Restart(ctx, o)
	case "fw allow", "fw deny":
		err = a.Fw(ctx, o, append([]string{act.Sub}, act.Args...))
	case "repo add", "repo rm", "repo sync":
		err = a.Repo(ctx, act.Sub, o, act.Args)
	case "backup ":
		if root.BackupNeedsPrompt() {
			return "", &ops.Error{Kind: ops.KindState, Code: 1, Msg: "there is no backup key, so a backup would ask for a passphrase", Hint: "run: berth keygen"}
		}
		err = root.Backup(ctx, []string{o})
	default:
		err = fmt.Errorf("the dashboard has no action %q", act.Label())
	}
	text := strings.TrimRight(out.String(), "\n")
	if e := strings.TrimRight(errb.String(), "\n"); e != "" {
		text = strings.TrimLeft(text+"\n"+e, "\n")
	}
	return text, err
}

// Handoff runs berth itself with the terminal, so attach and shell behave as the commands do.
func (b *appBackend) Handoff(ctx context.Context, org, kind string) error {
	args := append(append([]string{b.self}, b.args...), kind, org)
	return b.app().Operator.Exec.Run(ctx, host.Cmd{Args: args, TTY: true})
}
