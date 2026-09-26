package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/tabwriter"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/host"
	sshhost "github.com/ar4mirez/berth/internal/host/ssh"
	"github.com/ar4mirez/berth/internal/hosts"
	"github.com/ar4mirez/berth/internal/ops"
)

// The host registry (#44): berth host add|ls|rm|rotate-access. The registry, berth's keys and its
// known_hosts are on the operator's machine (a.Operator), whichever host an org is on.

const hostTimeout = 10 * time.Second

func (a *App) hostPaths() hosts.Paths {
	cfg := a.Getenv("XDG_CONFIG_HOME")
	if !path.IsAbs(cfg) {
		cfg = a.Getenv("HOME") + "/.config"
	}
	return hosts.Paths{Dir: cfg + "/" + Tool}
}

// lockHosts serializes changes to the registry and to berth's keys.
func (a *App) lockHosts(ctx context.Context) (func(), error) {
	p := a.hostPaths()
	if err := a.Operator.FS.MkdirAll(p.Dir, 0o700); err != nil {
		return nil, err
	}
	u, err := a.Operator.FS.Lock(ctx, p.Lock())
	if err != nil {
		return nil, err
	}
	return func() { _ = u.Unlock() }, nil
}

// dialHost connects to a registered host with berth's own key for it, and nothing else.
func (a *App) dialHost(ctx context.Context, e hosts.Entry, key string) (*host.Host, error) {
	return sshhost.Dial(ctx, sshhost.Config{
		Addr: e.Addr, User: e.User, KnownHosts: a.hostPaths().KnownHosts(),
		IdentityFiles: []string{key}, NoAgent: true, Timeout: hostTimeout,
	}, a.Operator)
}

// remoteOut runs args on h and returns its trimmed stdout; a failure carries its stderr.
func remoteOut(ctx context.Context, h *host.Host, args ...string) (string, error) {
	var out, errb bytes.Buffer
	if err := h.Exec.Run(ctx, host.Cmd{Args: args, Stdout: &out, Stderr: &errb}); err != nil {
		if host.IsNotFound(err) {
			return "", fmt.Errorf("%s isn't installed", args[0])
		}
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, firstLineOf(msg))
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

func firstLineOf(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// dockerVersion is the engine's version on h, which also proves this user may use it.
func dockerVersion(ctx context.Context, h *host.Host) (string, error) {
	return remoteOut(ctx, h, "docker", "version", "--format", "{{.Server.Version}}")
}

// orgsOn lists the orgs under home on h (directories with an org.env).
func orgsOn(h *host.Host, home string) ([]string, error) {
	dir := path.Join(home, "orgs")
	ents, err := h.FS.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		if fi, err := h.FS.Stat(path.Join(dir, e.Name(), "org.env")); err == nil && fi.Mode().IsRegular() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// remoteHome is $HOME on h.
func remoteHome(ctx context.Context, h *host.Host) (string, error) {
	home, err := remoteOut(ctx, h, "sh", "-c", `printf '%s' "$HOME"`)
	if err != nil {
		return "", err
	}
	if !path.IsAbs(home) {
		return "", fmt.Errorf("the remote $HOME is %q, not an absolute path", home)
	}
	return home, nil
}

// editAuthorized rewrites the authorized_keys of the user berth logs in as on h.
func editAuthorized(ctx context.Context, h *host.Host, edit func([]byte) []byte) error {
	home, err := remoteHome(ctx, h)
	if err != nil {
		return err
	}
	if err := h.FS.MkdirAll(home+"/.ssh", 0o700); err != nil {
		return err
	}
	f := home + "/.ssh/authorized_keys"
	old, err := h.FS.ReadFile(f)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next := edit(old)
	if bytes.Equal(next, old) {
		return nil
	}
	return h.FS.WriteFile(f, next, 0o600)
}

// writeKey saves a key made for a host: the private key (0600) and its .pub.
func (a *App) writeKey(file string, k hosts.Key, comment string) error {
	if err := a.Operator.FS.MkdirAll(path.Dir(file), 0o700); err != nil {
		return err
	}
	if err := a.Operator.FS.WriteFileAtomic(file, k.Private, 0o600); err != nil {
		return err
	}
	return a.Operator.FS.WriteFileAtomic(file+".pub", []byte(k.AuthorizedLine(comment)+"\n"), 0o644)
}

func (a *App) removeKey(file string) {
	_ = a.Operator.FS.Remove(file)
	_ = a.Operator.FS.Remove(file + ".pub")
}

// HostAdd is `berth host add <name> <[user@]host[:port]> [--home <dir>] [--identity <file>]...
// [--fingerprint SHA256:…] [--accept-new-host-key]`.
func (a *App) HostAdd(ctx context.Context, args []string) (err error) {
	usage := fmt.Errorf("usage: %s host add <name> <[user@]host[:port]> [--home <remote state root>] [--identity <key file>] [--fingerprint SHA256:…] [--accept-new-host-key]", Tool)
	var pos, identities []string
	var home, fingerprint string
	acceptNew := false
	for i := 0; i < len(args); i++ {
		switch x := args[i]; x {
		case "--home", "--identity", "--fingerprint":
			if i+1 >= len(args) {
				return usage
			}
			i++
			switch v := nth(args, i); x {
			case "--home":
				home = v
			case "--identity":
				identities = append(identities, v)
			default:
				fingerprint = v
			}
		case "--accept-new-host-key":
			acceptNew = true
		default:
			if strings.HasPrefix(x, "-") {
				return usage
			}
			pos = append(pos, x)
		}
	}
	if len(pos) != 2 {
		return usage
	}
	name, target := pos[0], pos[1]
	if err := a.State.Writable("add a host"); err != nil {
		return err
	}
	if err := hosts.CheckName(name); err != nil {
		return err
	}
	if home != "" && !path.IsAbs(home) {
		return fmt.Errorf("--home %s: must be an absolute path on the host", home)
	}
	if fingerprint != "" && !strings.HasPrefix(fingerprint, "SHA256:") {
		return fmt.Errorf("--fingerprint %s: expected SHA256:… (as ssh-keygen -lf prints it)", fingerprint)
	}
	user := a.Getenv("USER")
	if user == "" {
		user = a.Getenv("LOGNAME")
	}
	user, addr, err := hosts.ParseTarget(target, user)
	if err != nil {
		return err
	}

	unlock, err := a.lockHosts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	p := a.hostPaths()
	reg, err := hosts.Load(a.Operator.FS, p)
	if err != nil {
		return err
	}
	if e, ok := reg.Find(name); ok {
		return fmt.Errorf("host %s is already registered (%s); remove it first: %s host rm %s", name, e.Address(), Tool, name)
	}

	// Whatever this adds is undone if a later step fails: the host is registered completely or not at all.
	var undo []func()
	defer func() {
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
	}()
	knownBefore, knownErr := a.Operator.FS.ReadFile(p.KnownHosts())
	undo = append(undo, func() {
		now, _ := a.Operator.FS.ReadFile(p.KnownHosts())
		switch {
		case bytes.Equal(now, knownBefore):
		case errors.Is(knownErr, fs.ErrNotExist):
			_ = a.Operator.FS.Remove(p.KnownHosts())
		case knownErr == nil:
			_ = a.Operator.FS.WriteFileAtomic(p.KnownHosts(), knownBefore, 0o600)
		}
	})

	// Connect with the operator's own access (ssh-agent, --identity), pinning the host key.
	cfg := sshhost.Config{
		Addr: addr, User: user, KnownHosts: p.KnownHosts(), IdentityFiles: identities,
		AcceptNewHostKey: acceptNew, ExpectFingerprint: fingerprint, Timeout: 15 * time.Second,
	}
	fmt.Fprintf(a.Stdout, "Connecting to %s@%s…\n", user, addr)
	h, err := sshhost.Dial(ctx, cfg, a.Operator)
	var ue *sshhost.UnknownHostError
	if errors.As(err, &ue) && fingerprint == "" && !acceptNew {
		if cfg.ExpectFingerprint, err = a.confirmHostKey(ue); err != nil {
			return err
		}
		fingerprint = cfg.ExpectFingerprint
		h, err = sshhost.Dial(ctx, cfg, a.Operator)
	}
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	// Check it can run orgs.
	dv, err := dockerVersion(ctx, h)
	if err != nil {
		return fmt.Errorf("docker on %s: %w (is %s in the docker group?)", name, err, user)
	}
	cv, err := remoteOut(ctx, h, "docker", "compose", "version", "--short")
	if err != nil {
		return fmt.Errorf("docker compose on %s: %w", name, err)
	}
	facts, err := h.Facts.Facts(ctx)
	if err != nil {
		return fmt.Errorf("facts about %s: %w", name, err)
	}
	ports, err := h.Facts.PortsInUse(ctx)
	if err != nil {
		return fmt.Errorf("ports in use on %s: %w", name, err)
	}
	rhome, err := remoteHome(ctx, h)
	if err != nil {
		return err
	}
	if home == "" {
		home = rhome + "/.local/share/" + Tool
	}
	home = path.Clean(home)

	// berth's own key for this host, so its access can be rotated and revoked on its own.
	e := hosts.Entry{Name: name, Kind: hosts.KindSSH, User: user, Addr: addr, Home: home, Key: p.Key(name)}
	k, err := hosts.NewKey(hosts.Comment(name))
	if err != nil {
		return err
	}
	if err := a.writeKey(e.Key, k, hosts.Comment(name)); err != nil {
		return err
	}
	undo = append(undo, func() { a.removeKey(e.Key) })
	line := k.AuthorizedLine(hosts.Comment(name))
	if err := editAuthorized(ctx, h, func(b []byte) []byte { return hosts.AddAuthorized(b, line) }); err != nil {
		return fmt.Errorf("adding berth's key to %s's authorized_keys: %w", name, err)
	}
	undo = append(undo, func() {
		_ = editAuthorized(context.WithoutCancel(ctx), h, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, k.Public); return out })
	})
	if err := a.checkKey(ctx, e, e.Key); err != nil {
		return fmt.Errorf("berth's new key doesn't log in to %s: %w; nothing was changed", name, err)
	}

	orgs, _ := orgsOn(h, home)
	reg.Hosts = append(reg.Hosts, e)
	if err := hosts.Save(a.Operator.FS, p, reg); err != nil {
		return err
	}
	ts := facts.TailscaleIP
	if ts == "" {
		ts = "not up"
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Added %s (%s).\n", name, e.Address())
	if fingerprint != "" {
		fmt.Fprintf(w, "  host key\t%s, pinned in %s\n", fingerprint, p.KnownHosts())
	} else {
		fmt.Fprintf(w, "  host key\tpinned in %s\n", p.KnownHosts())
	}
	fmt.Fprintf(w, "  docker\t%s, compose %s\n", dv, cv)
	fmt.Fprintf(w, "  user\tuid %d, gid %d, %s\n", facts.UID, facts.GID, facts.Arch)
	fmt.Fprintf(w, "  tailscale\t%s\n", ts)
	fmt.Fprintf(w, "  ports in use\t%d\n", len(ports))
	fmt.Fprintf(w, "  state root\t%s (%d orgs)\n", home, len(orgs))
	fmt.Fprintf(w, "  access\tberth's own key %s; its line in authorized_keys ends %q\n", e.Key, hosts.Comment(name))
	return w.Flush()
}

// checkKey logs in to e with key alone and runs a command.
func (a *App) checkKey(ctx context.Context, e hosts.Entry, key string) error {
	h, err := a.dialHost(ctx, e, key)
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()
	_, err = remoteOut(ctx, h, "true")
	return err
}

// confirmHostKey shows an unknown host's key and asks the operator to confirm it, on the terminal.
// Without one, it says how to pass the fingerprint instead.
func (a *App) confirmHostKey(ue *sshhost.UnknownHostError) (string, error) {
	file := strings.TrimPrefix(ue.KeyType, "ssh-")
	if strings.HasPrefix(file, "ecdsa") {
		file = "ecdsa"
	}
	check := fmt.Sprintf("on the host: ssh-keygen -lf /etc/ssh/ssh_host_%s_key.pub", file)
	tty, err := a.openTTY()
	if err != nil {
		return "", fmt.Errorf("%s isn't known to %s yet; it presents the %s key %s. Compare that with the host's own (%s), "+
			"then run this again with --fingerprint %s", ue.Host, Tool, ue.KeyType, ue.Fingerprint, check, ue.Fingerprint)
	}
	defer func() { _ = tty.Close() }()
	fmt.Fprintf(a.Stderr, "%s isn't known to %s yet. It presents this %s key:\n  %s\nCompare it with the host's own (%s).\nTrust it? [y/N] ",
		ue.Host, Tool, ue.KeyType, ue.Fingerprint, check)
	ans, _ := bufio.NewReader(tty).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
		return "", errors.New("host key not trusted; nothing was changed")
	}
	return ue.Fingerprint, nil
}

// HostLs is `berth host ls`: this machine and every registered host, whether it's reachable, its
// Docker version and its number of orgs.
func (a *App) HostLs(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: %s host ls", Tool)
	}
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		return err
	}
	res := ops.Hosts{Schema: "berth.hosts/v1", Hosts: []ops.HostStatus{a.statusOf(ctx, a.Operator, hosts.Entry{Name: hosts.Local, Kind: hosts.KindLocal, Home: a.State.Home.Path})}}
	for _, e := range reg.Hosts {
		h, err := a.dialHost(ctx, e, e.Key)
		if err != nil {
			res.Hosts = append(res.Hosts, ops.HostStatus{Name: e.Name, Kind: e.Kind, Address: e.Address(), Home: e.Home, Error: err.Error()})
			continue
		}
		res.Hosts = append(res.Hosts, a.statusOf(ctx, h, e))
		_ = h.Close()
	}
	if a.Output == OutputJSON {
		return a.writeJSON(res)
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tADDRESS\tREACHABLE\tDOCKER\tORGS")
	var problems []string
	for _, s := range res.Hosts {
		addr, reach, dv, orgs := s.Address, "yes", s.Docker, "-"
		if addr == "" {
			addr = "-"
		}
		if !s.Reachable {
			reach = "no"
		}
		if dv == "" {
			dv = "-"
		}
		if s.Orgs != nil {
			orgs = fmt.Sprint(*s.Orgs)
		}
		if s.Error != "" {
			problems = append(problems, s.Name+": "+s.Error)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Name, s.Kind, addr, reach, dv, orgs)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, p := range problems {
		fmt.Fprintln(a.Stderr, p)
	}
	return nil
}

func (a *App) statusOf(ctx context.Context, h *host.Host, e hosts.Entry) ops.HostStatus {
	s := ops.HostStatus{Name: e.Name, Kind: e.Kind, Address: e.Address(), Home: e.Home, Reachable: true}
	var errs []string
	if v, err := dockerVersion(ctx, h); err == nil {
		s.Docker = v
	} else {
		errs = append(errs, "docker: "+err.Error())
	}
	if names, err := orgsOn(h, e.Home); err == nil {
		n := len(names)
		s.Orgs = &n
	} else {
		errs = append(errs, "orgs: "+err.Error())
	}
	s.Error = strings.Join(errs, "; ")
	return s
}

// HostRm is `berth host rm <name> [--force]`: forget a host. It refuses while the host has orgs, or
// can't be reached to check, unless --force. Its orgs are never touched.
func (a *App) HostRm(ctx context.Context, args []string) error {
	var name string
	force := false
	for _, x := range args {
		switch {
		case x == "--force" || x == "-f":
			force = true
		case name == "" && !strings.HasPrefix(x, "-"):
			name = x
		default:
			return fmt.Errorf("usage: %s host rm <name> [--force]", Tool)
		}
	}
	if name == "" {
		return fmt.Errorf("usage: %s host rm <name> [--force]", Tool)
	}
	if err := a.State.Writable("remove a host"); err != nil {
		return err
	}
	if name == hosts.Local {
		return fmt.Errorf("%q is this machine; it can't be removed", hosts.Local)
	}
	unlock, err := a.lockHosts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	p := a.hostPaths()
	reg, err := hosts.Load(a.Operator.FS, p)
	if err != nil {
		return err
	}
	e, ok := reg.Find(name)
	if !ok {
		return fmt.Errorf("unknown host '%s' (see: %s host ls)", name, Tool)
	}

	keyLeft := false
	h, err := a.dialHost(ctx, e, e.Key)
	if err != nil {
		if !force {
			return fmt.Errorf("can't reach %s to check it has no orgs: %w\n(--force forgets it anyway)", name, err)
		}
		keyLeft = true
	} else {
		defer func() { _ = h.Close() }()
		orgs, err := orgsOn(h, e.Home)
		switch {
		case err != nil && !force:
			return fmt.Errorf("can't list %s's orgs: %w\n(--force forgets it anyway)", name, err)
		case len(orgs) > 0 && !force:
			return fmt.Errorf("%s has %d org(s) in %s: %s. Move or remove them first, or --force to forget the host anyway (they keep running there)",
				name, len(orgs), e.Home, strings.Join(orgs, ", "))
		}
		// Revoke berth's access: its line in authorized_keys, nothing else.
		if pub, err := a.hostKeyPub(e.Key); err == nil {
			if err := editAuthorized(ctx, h, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, pub); return out }); err != nil {
				keyLeft = true
			}
		} else {
			keyLeft = true
		}
	}
	reg.Remove(name)
	if err := hosts.Save(a.Operator.FS, p, reg); err != nil {
		return err
	}
	if err := sshhost.ForgetHost(a.Operator.FS, p.KnownHosts(), e.Addr); err != nil {
		fmt.Fprintf(a.Stderr, "warning: couldn't remove %s from %s: %v\n", e.Addr, p.KnownHosts(), err)
	}
	a.removeKey(e.Key)
	fmt.Fprintf(a.Stdout, "Removed %s (%s). Nothing on it was stopped.\n", name, e.Address())
	if keyLeft {
		fmt.Fprintf(a.Stdout, "berth's key may still be in its authorized_keys: remove the line ending %q there by hand.\n", hosts.Comment(name))
	}
	return nil
}

func (a *App) hostKeyPub(file string) (gossh.PublicKey, error) {
	b, err := a.Operator.FS.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return hosts.PublicOf(b)
}

// HostRotateAccess is `berth host rotate-access <name>`: replace berth's key on the host. The new
// key is added and must log in before the old one is removed, so access is never lost.
func (a *App) HostRotateAccess(ctx context.Context, args []string) (err error) {
	if len(args) != 1 || strings.HasPrefix(nth(args, 0), "-") {
		return fmt.Errorf("usage: %s host rotate-access <name>", Tool)
	}
	name := nth(args, 0)
	if err := a.State.Writable("rotate a host's access"); err != nil {
		return err
	}
	unlock, err := a.lockHosts(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		return err
	}
	e, ok := reg.Find(name)
	if !ok {
		return fmt.Errorf("unknown host '%s' (see: %s host ls)", name, Tool)
	}
	old, err := a.hostKeyPub(e.Key)
	if err != nil {
		return fmt.Errorf("berth's key for %s: %w", name, err)
	}
	h, err := a.dialHost(ctx, e, e.Key)
	if err != nil {
		return fmt.Errorf("can't reach %s with berth's current key: %w", name, err)
	}
	defer func() { _ = h.Close() }()

	k, err := hosts.NewKey(hosts.Comment(name))
	if err != nil {
		return err
	}
	next := e.Key + ".new"
	if err := a.writeKey(next, k, hosts.Comment(name)); err != nil {
		return err
	}
	line := k.AuthorizedLine(hosts.Comment(name))
	if err := editAuthorized(ctx, h, func(b []byte) []byte { return hosts.AddAuthorized(b, line) }); err != nil {
		a.removeKey(next)
		return fmt.Errorf("adding the new key to %s: %w; the old key still works", name, err)
	}
	if err := a.checkKey(ctx, e, next); err != nil {
		_ = editAuthorized(ctx, h, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, k.Public); return out })
		a.removeKey(next)
		return fmt.Errorf("the new key doesn't log in to %s: %w; the old key still works, nothing was changed", name, err)
	}
	// The new key works: switch to it, then revoke the old one over a connection made with it.
	if err := a.Operator.FS.Rename(next, e.Key); err != nil {
		return err
	}
	if err := a.Operator.FS.Rename(next+".pub", e.Key+".pub"); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "berth's key for %s is now %s.\n", name, gossh.FingerprintSHA256(k.Public))
	nh, err := a.dialHost(ctx, e, e.Key)
	if err == nil {
		defer func() { _ = nh.Close() }()
		err = editAuthorized(ctx, nh, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, old); return out })
	}
	if err != nil {
		fmt.Fprintf(a.Stderr, "warning: couldn't remove the old key (%s) from %s: %v. Remove that line from its authorized_keys by hand.\n",
			gossh.FingerprintSHA256(old), name, err)
		return nil
	}
	fmt.Fprintf(a.Stdout, "The old key (%s) was removed from %s.\n", gossh.FingerprintSHA256(old), name)
	return nil
}
