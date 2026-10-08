package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/cloud"
	"github.com/ar4mirez/berth/internal/hosts"
	"github.com/ar4mirez/berth/internal/ops"
)

// Cloud hosts (#52): `berth host create|destroy|reconcile`. The first provider is Hetzner Cloud
// (docs/decisions/051-first-cloud-provider.md).
//
// A host berth creates lets nothing in from the internet (a provider firewall with no rules) and
// is reached over Tailscale only. berth makes its ssh host key itself and pins it before the first
// connection. Everything it creates carries labels (berth.managed, berth.host), and is found
// again, and removed, by them.

// ProviderHetzner is the one provider there is.
const ProviderHetzner = "hetzner"

// The environment a create reads its secrets from: never the command line.
const (
	envHetznerToken  = "HCLOUD_TOKEN"
	envTailscaleKey  = "BERTH_TAILSCALE_AUTHKEY"
	envHetznerAPI    = "BERTH_HCLOUD_ENDPOINT" // another endpoint, for tests
	cloudFirewallTTL = 90 * time.Second        // how long a firewall may stay "in use" after its server is deleted
)

// CloudHost is a new host to register: where it answers, and what berth pinned for it.
type CloudHost struct {
	Name, Addr, KeyFile, Fingerprint, Home, Bind string
}

func (a *App) hetzner() (*cloud.Hetzner, error) {
	token := a.Getenv(envHetznerToken)
	if token == "" {
		return nil, &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "set " + envHetznerToken + " to an API token of the Hetzner Cloud project (read and write)",
			Hint: "a token is a project's: use a project for berth's hosts alone"}
	}
	return &cloud.Hetzner{Token: token, Endpoint: a.Getenv(envHetznerAPI)}, nil
}

func cloudLabels(name string) map[string]string {
	return map[string]string{cloud.LabelManaged: "true", cloud.LabelHost: name}
}

// HostCreate is `berth host create <name> --provider hetzner [--size T] [--region L] [--image I] [--home D] [--bind M]`.
func (a *App) HostCreate(ctx context.Context, args []string) error {
	usage := &ops.Error{Kind: ops.KindUsage, Code: 1,
		Msg: fmt.Sprintf("usage: %s host create <name> --provider hetzner [--size cax21] [--region fsn1] [--image ubuntu-24.04] [--home <dir>] [--bind <mode>]", Tool)}
	opt := map[string]string{"--provider": "", "--size": "cax21", "--region": "fsn1", "--image": "ubuntu-24.04", "--home": "", "--bind": ""}
	name := ""
	for i := 0; i < len(args); i++ {
		if _, ok := opt[args[i]]; ok && i+1 < len(args) {
			opt[args[i]] = args[i+1]
			i++
		} else if strings.HasPrefix(args[i], "-") || name != "" {
			return usage
		} else {
			name = args[i]
		}
	}
	switch {
	case name == "" || opt["--provider"] == "":
		return usage
	case opt["--provider"] != ProviderHetzner:
		return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("unknown provider %q: berth creates hosts at %s", opt["--provider"], ProviderHetzner)}
	}
	if err := a.State.Writable("create a host"); err != nil {
		return err
	}
	if err := hosts.CheckName(name); err != nil {
		return err
	}
	if _, err := a.registered(name); err == nil {
		return fmt.Errorf("there is already a host named %s (%s host ls)", name, Tool)
	}
	tsKey := a.Getenv(envTailscaleKey)
	if tsKey == "" {
		return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: "set " + envTailscaleKey + " to a single-use, tagged Tailscale auth key: the new host is reached over Tailscale only",
			Hint: "Tailscale admin console → Settings → Keys → Generate auth key (not reusable, with a tag)"}
	}
	h, err := a.hetzner()
	if err != nil {
		return err
	}
	// Something of that name left from before would be taken for this host's.
	if left, err := h.Servers(ctx, name); err != nil {
		return err
	} else if len(left) > 0 {
		return fmt.Errorf("the project already has a server labelled for %s: see %s host reconcile", name, Tool)
	}

	hostKey, err := hosts.NewKey("berth-" + name)
	if err != nil {
		return err
	}
	boot, err := hosts.NewKey(hosts.Comment(name) + " (first login)")
	if err != nil {
		return err
	}
	keyFile := a.hostPaths().Key(".create-" + name)
	if err := a.Operator.FS.MkdirAll(path.Dir(keyFile), 0o700); err != nil {
		return err
	}
	if err := a.Operator.FS.WriteFileAtomic(keyFile, boot.Private, 0o600); err != nil {
		return err
	}
	defer func() { _ = a.Operator.FS.Remove(keyFile) }()

	vm := "berth-" + name
	init := cloud.Init{
		Hostname: vm, HostKeyPrivate: string(hostKey.Private), HostKeyPublic: strings.TrimSpace(string(gossh.MarshalAuthorizedKey(hostKey.Public))),
		AuthorizedKey: boot.AuthorizedLine(hosts.Comment(name)), TailscaleAuthKey: tsKey,
	}
	// From here on, a failure removes what was created: nothing is left behind half-made.
	undo := func(why error) error {
		removed, rerr := h.Remove(context.WithoutCancel(ctx), name, cloudFirewallTTL)
		if rerr != nil {
			return fmt.Errorf("%w; AND removing what was created failed: %w (see: %s host reconcile)", why, rerr, Tool)
		}
		if len(removed) > 0 {
			sayf(a.Stderr, "%s: removed %s\n", Tool, strings.Join(removed, ", "))
		}
		return why
	}
	sayf(a.Stdout, "Creating %s at Hetzner (%s, %s, %s), with nothing open to the internet…\n", vm, opt["--size"], opt["--region"], opt["--image"])
	fw, err := h.CreateFirewall(ctx, vm, cloudLabels(name))
	if err != nil {
		return undo(fmt.Errorf("creating the firewall: %w", err))
	}
	srv, err := h.CreateServer(ctx, cloud.ServerSpec{Name: vm, Type: opt["--size"], Location: opt["--region"], Image: opt["--image"],
		UserData: init.UserData(), Labels: cloudLabels(name), Firewall: fw.ID})
	if err != nil {
		return undo(fmt.Errorf("creating the server: %w", err))
	}
	sayf(a.Stdout, "Server %d is starting. It installs Docker and joins your tailnet as %s (a few minutes)…\n", srv.ID, vm)
	register := a.CloudRegister
	if register == nil {
		register = a.registerCloudHost
	}
	fingerprint := gossh.FingerprintSHA256(hostKey.Public)
	if err := register(ctx, CloudHost{Name: name, Addr: vm, KeyFile: keyFile, Fingerprint: fingerprint, Home: opt["--home"], Bind: opt["--bind"]}); err != nil {
		return undo(fmt.Errorf("%s never became a berth host: %w", vm, err))
	}
	// The registry says where the host came from: destroy and reconcile go by it.
	p := a.hostPaths()
	unlock, err := a.lockHosts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	reg, err := hosts.Load(a.Operator.FS, p)
	if err != nil {
		return err
	}
	for i := range reg.Hosts {
		if reg.Hosts[i].Name == name {
			reg.Hosts[i].Provider = ProviderHetzner
		}
	}
	if err := hosts.Save(a.Operator.FS, p, reg); err != nil {
		return err
	}
	sayf(a.Stdout, "%s is a berth host (%s at Hetzner; ssh host key %s, pinned before the first connection).\n", name, vm, fingerprint)
	return nil
}

// registerCloudHost waits for the new host on the tailnet, then registers it as host add does,
// with the host key berth made already pinned.
func (a *App) registerCloudHost(ctx context.Context, c CloudHost) error {
	deadline := time.Now().Add(12 * time.Minute)
	for {
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(c.Addr, "22"))
		if err == nil {
			_ = conn.Close()
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s:22 didn't answer in 12 minutes (is this machine on the tailnet?): %w", c.Addr, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}
	args := []string{c.Name, "ops@" + c.Addr, "--identity", c.KeyFile, "--fingerprint", c.Fingerprint}
	if c.Home != "" {
		args = append(args, "--home", c.Home)
	}
	if c.Bind != "" {
		args = append(args, "--bind", c.Bind)
	}
	return a.HostAdd(ctx, args)
}

// HostDestroy is `berth host destroy <name> [--force]`: the host leaves the registry as with host
// rm (refused while it has orgs, unless forced), and its server and firewall are deleted.
func (a *App) HostDestroy(ctx context.Context, args []string) error {
	usage := &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("usage: %s host destroy <name> [--force]", Tool)}
	name, force := "", false
	for _, x := range args {
		switch {
		case x == "--force" || x == "-f":
			force = true
		case strings.HasPrefix(x, "-") || name != "":
			return usage
		default:
			name = x
		}
	}
	if name == "" {
		return usage
	}
	if err := a.State.Writable("destroy a host"); err != nil {
		return err
	}
	e, err := a.registered(name)
	if err != nil {
		return err
	}
	if e.Provider != ProviderHetzner {
		return fmt.Errorf("%s wasn't created by berth: %s host rm %s forgets it, and its machine is yours to remove", name, Tool, name)
	}
	h, err := a.hetzner()
	if err != nil {
		return err
	}
	rm := []string{name}
	if force {
		rm = append(rm, "--force")
	}
	if err := a.HostRm(ctx, rm); err != nil {
		return err
	}
	removed, err := h.Remove(ctx, name, cloudFirewallTTL)
	if len(removed) > 0 {
		sayf(a.Stdout, "Deleted at Hetzner: %s.\n", strings.Join(removed, ", "))
	}
	if err != nil {
		return fmt.Errorf("%w (%s host reconcile shows what is left)", err, Tool)
	}
	if len(removed) == 0 {
		sayf(a.Stdout, "Nothing labelled for %s was left at Hetzner.\n", name)
	}
	return nil
}

// HostReconcile is `berth host reconcile [--prune]`: what berth's labels are on at the provider,
// against the registry. --prune deletes what belongs to no registered host.
func (a *App) HostReconcile(ctx context.Context, args []string) error {
	prune := slices.Contains(args, "--prune")
	if len(args) > 1 || (len(args) == 1 && !prune) {
		return &ops.Error{Kind: ops.KindUsage, Code: 1, Msg: fmt.Sprintf("usage: %s host reconcile [--prune]", Tool)}
	}
	if prune {
		if err := a.State.Writable("delete cloud resources"); err != nil {
			return err
		}
	}
	h, err := a.hetzner()
	if err != nil {
		return err
	}
	servers, err := h.Servers(ctx, "")
	if err != nil {
		return err
	}
	fws, err := h.Firewalls(ctx, "")
	if err != nil {
		return err
	}
	have := map[string][]string{}
	for _, s := range servers {
		have[s.Labels[cloud.LabelHost]] = append(have[s.Labels[cloud.LabelHost]], "server "+s.Name)
	}
	for _, f := range fws {
		have[f.Labels[cloud.LabelHost]] = append(have[f.Labels[cloud.LabelHost]], "firewall "+f.Name)
	}
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		return err
	}
	var names []string
	for n := range have {
		names = append(names, n)
	}
	sort.Strings(names)
	var orphans []string
	for _, n := range names {
		e, ok := reg.Find(n)
		state := "registered"
		if !ok || e.Provider != ProviderHetzner {
			state, orphans = "NOT a registered host", append(orphans, n)
		}
		sayf(a.Stdout, "%-20s %-22s %s\n", n, state, strings.Join(have[n], ", "))
	}
	for _, e := range reg.Hosts {
		if e.Provider == ProviderHetzner && len(have[e.Name]) == 0 {
			sayf(a.Stdout, "%-20s %-22s %s\n", e.Name, "registered", "nothing at Hetzner: its server is gone ("+Tool+" host rm "+e.Name+" --force)")
		}
	}
	switch {
	case len(names) == 0:
		sayf(a.Stdout, "Nothing at Hetzner carries berth's labels.\n")
	case len(orphans) == 0:
		sayf(a.Stdout, "Everything berth made at Hetzner belongs to a registered host.\n")
	case !prune:
		sayf(a.Stdout, "%d left behind (%s). Delete them: %s host reconcile --prune\n", len(orphans), strings.Join(orphans, ", "), Tool)
		return &Exit{Code: 3}
	}
	var errs []error
	for _, n := range orphans {
		if !prune {
			break
		}
		removed, err := h.Remove(ctx, n, cloudFirewallTTL)
		if len(removed) > 0 {
			sayf(a.Stdout, "Deleted %s: %s.\n", n, strings.Join(removed, ", "))
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
