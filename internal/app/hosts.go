package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
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

// dialHost connects to a registered host with berth's own key for it, and nothing else: through
// its jump host too, where that key may only forward to it (#99).
func (a *App) dialHost(ctx context.Context, e hosts.Entry, key string) (*host.Host, error) {
	h, err := sshhost.Dial(ctx, a.hostConfig(e, key), a.Operator)
	if err != nil {
		return nil, err
	}
	return host.WithEngine(h, e.Engine), nil
}

// hostConfig is how berth reaches e with key alone.
func (a *App) hostConfig(e hosts.Entry, key string) sshhost.Config {
	cfg := sshhost.Config{
		Addr: e.Addr, User: e.User, KnownHosts: a.hostPaths().KnownHosts(),
		IdentityFiles: []string{key}, NoAgent: true, Timeout: hostTimeout,
	}
	if user, addr := e.JumpTarget(); addr != "" {
		cfg.Jump = &sshhost.Config{Addr: addr, User: user, IdentityFiles: []string{key}, NoAgent: true}
	}
	return cfg
}

// dialJump connects to e's jump host with the operator's own access (ssh-agent, identities):
// berth's key there may only forward, so berth's line in its authorized_keys is managed with the
// operator's (#99).
func (a *App) dialJump(ctx context.Context, e hosts.Entry, identities []string) (*host.Host, error) {
	user, addr := e.JumpTarget()
	h, err := sshhost.Dial(ctx, sshhost.Config{
		Addr: addr, User: user, KnownHosts: a.hostPaths().KnownHosts(), IdentityFiles: identities, Timeout: hostTimeout,
	}, a.Operator)
	if err != nil {
		return nil, fmt.Errorf("the jump host %s, with your own access (ssh-agent, or --identity): %w", e.Jump, err)
	}
	return h, nil
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

// dockerVersion is the engine's version on h, which also proves this user may use it. (Podman
// run locally has no Server part in its version.)
func dockerVersion(ctx context.Context, h *host.Host) (string, error) {
	if h.EngineName() == host.EnginePodman {
		return remoteOut(ctx, h, "docker", "version", "--format", "{{.Client.Version}}")
	}
	return remoteOut(ctx, h, "docker", "version", "--format", "{{.Server.Version}}")
}

// detectEngine is the engine a new host runs: Docker if its CLI answers, else Podman.
func detectEngine(ctx context.Context, h *host.Host) string {
	if _, err := dockerVersion(ctx, h); err == nil {
		return host.EngineDocker
	}
	if _, err := dockerVersion(ctx, host.WithEngine(h, host.EnginePodman)); err == nil {
		return host.EnginePodman
	}
	return host.EngineDocker // for the error that follows
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
	usage := fmt.Errorf("usage: %s host add <name> <[user@]host[:port]> [--home <remote state root>] [--identity <key file>] [--fingerprint SHA256:…] [--accept-new-host-key] [--engine docker|podman] [--bind <mode>] [--no-guard] [--jump <[user@]host[:port]> [--jump-fingerprint SHA256:…]]", Tool)
	var pos, identities []string
	var home, fingerprint, engine, bind, jump, jumpFingerprint string
	acceptNew, guard := false, true
	for i := 0; i < len(args); i++ {
		switch x := args[i]; x {
		case "--home", "--identity", "--fingerprint", "--engine", "--bind", "--jump", "--jump-fingerprint":
			if i+1 >= len(args) {
				return usage
			}
			i++
			switch v := nth(args, i); x {
			case "--home":
				home = v
			case "--identity":
				identities = append(identities, v)
			case "--engine":
				engine = v
			case "--bind":
				bind = v
			case "--jump":
				jump = v
			case "--jump-fingerprint":
				jumpFingerprint = v
			default:
				fingerprint = v
			}
		case "--accept-new-host-key":
			acceptNew = true
		case "--no-guard":
			guard = false
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
	if err := host.CheckEngine(engine); err != nil {
		return err
	}
	if engine == host.EngineApple {
		return fmt.Errorf("the engine \"container\" (Apple's) is for this machine (engine: container in config.yaml): a registered host runs docker or podman")
	}
	if err := CheckBind(bind); err != nil {
		return err
	}
	if fingerprint != "" && !strings.HasPrefix(fingerprint, "SHA256:") {
		return fmt.Errorf("--fingerprint %s: expected SHA256:… (as ssh-keygen -lf prints it)", fingerprint)
	}
	if jumpFingerprint != "" && !strings.HasPrefix(jumpFingerprint, "SHA256:") {
		return fmt.Errorf("--jump-fingerprint %s: expected SHA256:… (as ssh-keygen -lf prints it)", jumpFingerprint)
	}
	if jumpFingerprint != "" && jump == "" {
		return errors.New("--jump-fingerprint is the jump host's: it needs --jump")
	}
	me := a.Getenv("USER")
	if me == "" {
		me = a.Getenv("LOGNAME")
	}
	user, addr, err := hosts.ParseTarget(target, me)
	if err != nil {
		return err
	}
	var jumpUser, jumpAddr string
	if jump != "" {
		if jumpUser, jumpAddr, err = hosts.ParseTarget(jump, me); err != nil {
			return fmt.Errorf("--jump %s: %w", jump, err)
		}
		if jumpAddr == addr {
			return fmt.Errorf("--jump %s is the host itself", jump)
		}
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
	if jump != "" {
		// The jump host's key is pinned the same way, and the operator's access logs in there too.
		cfg.Jump = &sshhost.Config{Addr: jumpAddr, User: jumpUser, IdentityFiles: identities,
			AcceptNewHostKey: acceptNew, ExpectFingerprint: jumpFingerprint}
		sayf(a.Stdout, "Connecting to %s@%s through %s@%s…\n", user, addr, jumpUser, jumpAddr)
	} else {
		sayf(a.Stdout, "Connecting to %s@%s…\n", user, addr)
	}
	h, err := sshhost.Dial(ctx, cfg, a.Operator)
	// An unknown key is confirmed on the terminal: the jump host's first, then the host's.
	for range 2 {
		var ue *sshhost.UnknownHostError
		if !errors.As(err, &ue) || acceptNew {
			break
		}
		switch {
		case cfg.Jump != nil && ue.Host == jumpAddr && jumpFingerprint == "":
			if jumpFingerprint, err = a.confirmHostKey(ue, "--jump-fingerprint"); err != nil {
				return err
			}
			cfg.Jump.ExpectFingerprint = jumpFingerprint
		case ue.Host == addr && fingerprint == "":
			if fingerprint, err = a.confirmHostKey(ue, "--fingerprint"); err != nil {
				return err
			}
			cfg.ExpectFingerprint = fingerprint
		default:
			return err
		}
		h, err = sshhost.Dial(ctx, cfg, a.Operator)
	}
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	// Check it can run orgs, with its engine (--engine, else the one it has: Docker, else Podman).
	if engine == "" {
		engine = detectEngine(ctx, h)
	}
	h = host.WithEngine(h, engine)
	dv, err := dockerVersion(ctx, h)
	if err != nil {
		hint := "is " + user + " in the docker group?"
		if engine == host.EnginePodman {
			hint = "does podman work for " + user + "?"
		}
		return fmt.Errorf("%s on %s: %w (%s)", engine, name, err, hint)
	}
	cv, err := remoteOut(ctx, h, "docker", "compose", "version", "--short")
	if err != nil {
		return fmt.Errorf("%s compose on %s: %w", engine, name, err)
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
	if engine != host.EngineDocker {
		e.Engine = engine
	}
	e.Bind = bind
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
	if jump != "" {
		// On the jump host the same key may only forward to this host (#99).
		e.Jump = jumpUser + "@" + jumpAddr
		jh, err := a.dialJump(ctx, e, identities)
		if err != nil {
			return err
		}
		defer func() { _ = jh.Close() }()
		jline := hosts.JumpLine(k, hosts.Comment(name), addr)
		if err := editAuthorized(ctx, jh, func(b []byte) []byte { return hosts.AddAuthorized(b, jline) }); err != nil {
			return fmt.Errorf("adding berth's key to the jump host %s's authorized_keys: %w", e.Jump, err)
		}
		undo = append(undo, func() {
			_ = editAuthorized(context.WithoutCancel(ctx), jh, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, k.Public); return out })
		})
	}
	if err := a.checkKey(ctx, e, e.Key); err != nil {
		return fmt.Errorf("berth's new key doesn't log in to %s: %w; nothing was changed", name, err)
	}

	// The host guard (#48), unless the operator opted out.
	guarded := "off (--no-guard)"
	if why := guardUnavailable(ctx, a.On(name, h, home)); guard && why != "" {
		guarded = why
	} else if guard {
		b := a.On(name, h, home)
		if err := a.installGuard(ctx, b); err != nil {
			_ = a.removeGuard(context.WithoutCancel(ctx), b)
			return fmt.Errorf("the host guard on %s: %w (--no-guard skips it)", name, err)
		}
		undo = append(undo, func() { _ = a.removeGuard(context.WithoutCancel(ctx), b) })
		guarded = "on: org containers can't reach the host or 169.254.0.0/16"
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
	fmt.Fprintf(w, "  engine\t%s %s, compose %s\n", engine, dv, cv)
	fmt.Fprintf(w, "  user\tuid %d, gid %d, %s\n", facts.UID, facts.GID, facts.Arch)
	fmt.Fprintf(w, "  tailscale\t%s\n", ts)
	fmt.Fprintf(w, "  ports in use\t%d\n", len(ports))
	fmt.Fprintf(w, "  state root\t%s (%d orgs)\n", home, len(orgs))
	fmt.Fprintf(w, "  access\tberth's own key %s; its line in authorized_keys ends %q\n", e.Key, hosts.Comment(name))
	if e.Jump != "" {
		fmt.Fprintf(w, "  jump\t%s; berth's key there may only forward to %s\n", e.Jump, addr)
	}
	fmt.Fprintf(w, "  guard\t%s\n", guarded)
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
// Without one, it says how to pass the fingerprint instead, with flag (--fingerprint, or
// --jump-fingerprint for a jump host's).
func (a *App) confirmHostKey(ue *sshhost.UnknownHostError, flag string) (string, error) {
	file := strings.TrimPrefix(ue.KeyType, "ssh-")
	if strings.HasPrefix(file, "ecdsa") {
		file = "ecdsa"
	}
	check := fmt.Sprintf("on the host: ssh-keygen -lf /etc/ssh/ssh_host_%s_key.pub", file)
	tty, err := a.openTTY()
	if err != nil {
		return "", fmt.Errorf("%s isn't known to %s yet; it presents the %s key %s. Compare that with the host's own (%s), "+
			"then run this again with %s %s", ue.Host, Tool, ue.KeyType, ue.Fingerprint, check, flag, ue.Fingerprint)
	}
	defer func() { _ = tty.Close() }()
	sayf(a.Stderr, "%s isn't known to %s yet. It presents this %s key:\n  %s\nCompare it with the host's own (%s).\nTrust it? [y/N] ",
		ue.Host, Tool, ue.KeyType, ue.Fingerprint, check)
	ans, _ := bufio.NewReader(tty).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(ans)); a != "y" && a != "yes" {
		return "", errors.New("host key not trusted; nothing was changed")
	}
	return ue.Fingerprint, nil
}

// Hosts is ops.System's: this machine and every registered host, whether it's reachable, its
// engine's version and its number of orgs.
func (a *App) Hosts(ctx context.Context) ([]ops.HostStatus, error) {
	reg, err := hosts.Load(a.Operator.FS, a.hostPaths())
	if err != nil {
		return nil, err
	}
	out := []ops.HostStatus{a.statusOf(ctx, a.Operator, hosts.Entry{Name: hosts.Local, Kind: hosts.KindLocal, Home: a.State.Home.Path, Engine: a.State.Engine})}
	for _, e := range reg.Hosts {
		h, err := a.dialHost(ctx, e, e.Key)
		if err != nil {
			eng := e.Engine
			if eng == "" {
				eng = host.EngineDocker
			}
			out = append(out, ops.HostStatus{Name: e.Name, Kind: e.Kind, Address: e.Address(), Home: e.Home, Engine: eng, Error: err.Error(), Jump: e.Jump})
			continue
		}
		out = append(out, a.statusOf(ctx, h, e))
		_ = h.Close()
	}
	return out, nil
}

// HostLs is `berth host ls`.
func (a *App) HostLs(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("usage: %s host ls", Tool)
	}
	res, err := ops.GetHosts(ctx, a)
	if err != nil {
		return err
	}
	if a.Output == OutputJSON {
		return a.writeJSON(res)
	}
	w := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tADDRESS\tREACHABLE\tENGINE\tORGS")
	var problems []string
	for _, s := range res.Hosts {
		addr, reach, dv, orgs := s.Address, "yes", s.Docker, "-"
		if addr == "" {
			addr = "-"
		}
		if s.Jump != "" {
			addr += " via " + s.Jump
		}
		if !s.Reachable {
			reach = "no"
		}
		if dv == "" {
			dv = "-"
		}
		dv = s.Engine + " " + dv
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
	s := ops.HostStatus{Name: e.Name, Kind: e.Kind, Address: e.Address(), Home: e.Home, Reachable: true, Engine: h.EngineName(), Jump: e.Jump}
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

// HostRm is `berth host rm <name> [--force] [--identity <key file>]...`: forget a host. It refuses
// while the host has orgs, or can't be reached to check, unless --force. Its orgs are never
// touched. --identity is the operator's access to its jump host, if it has one (#99).
func (a *App) HostRm(ctx context.Context, args []string) error {
	usage := fmt.Errorf("usage: %s host rm <name> [--force] [--identity <key file for its jump host>]", Tool)
	var name string
	var identities []string
	force := false
	for i := 0; i < len(args); i++ {
		switch x := args[i]; {
		case x == "--force" || x == "-f":
			force = true
		case x == "--identity" && i+1 < len(args):
			i++
			identities = append(identities, args[i])
		case name == "" && !strings.HasPrefix(x, "-"):
			name = x
		default:
			return usage
		}
	}
	if name == "" {
		return usage
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
		// Take the host guard's rules out, then revoke berth's access (its line in authorized_keys).
		if err := a.removeGuard(ctx, a.On(name, h, e.Home)); err != nil {
			sayf(a.Stderr, "warning: couldn't remove the host guard from %s: %v (there: docker rm -f %s)\n", name, err, hosts.GuardContainer)
		}
		if pub, err := a.hostKeyPub(e.Key); err == nil {
			if err := editAuthorized(ctx, h, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, pub); return out }); err != nil {
				keyLeft = true
			}
		} else {
			keyLeft = true
		}
	}
	// berth's line on the jump host goes with the operator's own access there (#99).
	jumpLeft := false
	if e.Jump != "" {
		pub, err := a.hostKeyPub(e.Key)
		if err == nil {
			var jh *host.Host
			if jh, err = a.dialJump(ctx, e, identities); err == nil {
				err = editAuthorized(ctx, jh, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, pub); return out })
				_ = jh.Close()
			}
		}
		if err != nil {
			sayf(a.Stderr, "warning: couldn't remove berth's key from the jump host: %v\n", err)
			jumpLeft = true
		}
	}
	reg.Remove(name)
	if err := hosts.Save(a.Operator.FS, p, reg); err != nil {
		return err
	}
	// A host key stays pinned while another host is reached at that address, or through it.
	_, jumpAddr := e.JumpTarget()
	for _, addr := range []string{e.Addr, jumpAddr} {
		if addr == "" || slices.ContainsFunc(reg.Hosts, func(o hosts.Entry) bool { _, j := o.JumpTarget(); return o.Addr == addr || j == addr }) {
			continue
		}
		if err := sshhost.ForgetHost(a.Operator.FS, p.KnownHosts(), addr); err != nil {
			sayf(a.Stderr, "warning: couldn't remove %s from %s: %v\n", addr, p.KnownHosts(), err)
		}
	}
	a.removeKey(e.Key)
	sayf(a.Stdout, "Removed %s (%s). Nothing on it was stopped.\n", name, e.Address())
	if orgs, err := a.dropLeases(name); err != nil {
		sayf(a.Stderr, "warning: couldn't drop %s's leases: %v\n", name, err)
	} else if len(orgs) > 0 {
		sort.Strings(orgs)
		sayf(a.Stdout, "It held the lease for %s: the next up on another host takes it (with --take-lease if the org is elsewhere too).\n", strings.Join(orgs, ", "))
	}
	if keyLeft {
		sayf(a.Stdout, "berth's key may still be in its authorized_keys: remove the line ending %q there by hand.\n", hosts.Comment(name))
		sayf(a.Stdout, "The host guard may still run there too: docker rm -f %s on it (its rules then last until the host reboots; docs/hosts.md).\n", hosts.GuardContainer)
	}
	if jumpLeft {
		sayf(a.Stdout, "berth's key may still be in the jump host %s's authorized_keys: remove the line ending %q there by hand.\n", e.Jump, hosts.Comment(name))
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

// HostRotateAccess is `berth host rotate-access <name> [--identity <key file>]...`: replace berth's
// key on the host. The new key is added and must log in before the old one is removed, so access
// is never lost. A host reached through a jump host has berth's key there too (#99), which the
// operator's own access to the jump host (ssh-agent, --identity) replaces the same way.
func (a *App) HostRotateAccess(ctx context.Context, args []string) (err error) {
	usage := fmt.Errorf("usage: %s host rotate-access <name> [--identity <key file for its jump host>]", Tool)
	var name string
	var identities []string
	for i := 0; i < len(args); i++ {
		switch x := args[i]; {
		case x == "--identity" && i+1 < len(args):
			i++
			identities = append(identities, args[i])
		case name == "" && !strings.HasPrefix(x, "-"):
			name = x
		default:
			return usage
		}
	}
	if name == "" {
		return usage
	}
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
	var jh *host.Host
	if e.Jump != "" {
		if jh, err = a.dialJump(ctx, e, identities); err != nil {
			return fmt.Errorf("berth's key on the jump host is replaced with your own access there: %w; nothing was changed", err)
		}
		defer func() { _ = jh.Close() }()
	}

	k, err := hosts.NewKey(hosts.Comment(name))
	if err != nil {
		return err
	}
	next := e.Key + ".new"
	if err := a.writeKey(next, k, hosts.Comment(name)); err != nil {
		return err
	}
	drop := func(h *host.Host, key gossh.PublicKey) error {
		return editAuthorized(ctx, h, func(b []byte) []byte { out, _ := hosts.RemoveAuthorized(b, key); return out })
	}
	line := k.AuthorizedLine(hosts.Comment(name))
	if err := editAuthorized(ctx, h, func(b []byte) []byte { return hosts.AddAuthorized(b, line) }); err != nil {
		a.removeKey(next)
		return fmt.Errorf("adding the new key to %s: %w; the old key still works", name, err)
	}
	if jh != nil {
		jline := hosts.JumpLine(k, hosts.Comment(name), e.Addr)
		if err := editAuthorized(ctx, jh, func(b []byte) []byte { return hosts.AddAuthorized(b, jline) }); err != nil {
			_ = drop(h, k.Public)
			a.removeKey(next)
			return fmt.Errorf("adding the new key to the jump host %s: %w; the old key still works", e.Jump, err)
		}
	}
	if err := a.checkKey(ctx, e, next); err != nil {
		_ = drop(h, k.Public)
		if jh != nil {
			_ = drop(jh, k.Public)
		}
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
	sayf(a.Stdout, "berth's key for %s is now %s.\n", name, gossh.FingerprintSHA256(k.Public))
	nh, err := a.dialHost(ctx, e, e.Key)
	if err == nil {
		defer func() { _ = nh.Close() }()
		err = drop(nh, old)
	}
	if err != nil {
		sayf(a.Stderr, "warning: couldn't remove the old key (%s) from %s: %v. Remove that line from its authorized_keys by hand.\n",
			gossh.FingerprintSHA256(old), name, err)
		return nil
	}
	if jh != nil {
		if err := drop(jh, old); err != nil {
			sayf(a.Stderr, "warning: couldn't remove the old key (%s) from the jump host %s: %v. Remove that line from its authorized_keys by hand.\n",
				gossh.FingerprintSHA256(old), e.Jump, err)
			return nil
		}
		sayf(a.Stdout, "The old key (%s) was removed from %s and from the jump host %s.\n", gossh.FingerprintSHA256(old), name, e.Jump)
		return nil
	}
	sayf(a.Stdout, "The old key (%s) was removed from %s.\n", gossh.FingerprintSHA256(old), name)
	return nil
}
