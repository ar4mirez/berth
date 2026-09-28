package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host"
)

// Every call below uses exactly the argv legacy/ccenv uses, and the same stdio routing: a stream
// ccenv sends to /dev/null is discarded, one it captures with $( ) is captured, and anything else
// goes straight to berth's own stdout/stderr, as in the shell.

// captureRaw is capture without stripping trailing newlines, for output that's processed further.
func (a *App) captureRaw(ctx context.Context, quiet bool, args ...string) (string, error) {
	var out bytes.Buffer
	stderr := a.Stderr
	if quiet {
		stderr = io.Discard
	}
	err := a.Host.Exec.Run(ctx, host.Cmd{Args: args, Stdout: &out, Stderr: stderr})
	return out.String(), err
}

// capture is `$(cmd)`: stdout (trailing newlines stripped); stderr goes to berth's stderr unless
// quiet (`2>/dev/null`). err is non-nil if the command failed or exited non-zero.
func (a *App) capture(ctx context.Context, quiet bool, args ...string) (string, error) {
	out, err := a.captureRaw(ctx, quiet, args...)
	return strings.TrimRight(out, "\n"), err
}

// passthrough runs a command with berth's own stdout/stderr (stdout discarded if quiet).
func (a *App) passthrough(ctx context.Context, quiet bool, args ...string) error {
	stdout := a.Stdout
	if quiet {
		stdout = io.Discard
	}
	return a.Host.Exec.Run(ctx, host.Cmd{Args: args, Stdout: stdout, Stderr: a.Stderr})
}

// running is `docker ps --format '{{.Names}}' 2>/dev/null | grep -qx "claude-$1"` (under pipefail,
// a failing docker ps means not running even if it printed the name).
func (a *App) running(ctx context.Context, o string) bool {
	out, err := a.capture(ctx, true, "docker", "ps", "--format", "{{.Names}}")
	if err != nil {
		return false
	}
	for _, l := range strings.Split(out, "\n") {
		if l == "claude-"+o {
			return true
		}
	}
	return false
}

// rcLoggedIn is `docker exec claude-$1 test -f /home/node/.claude/.credentials.json`.
func (a *App) rcLoggedIn(ctx context.Context, o string) bool {
	return a.passthrough(ctx, false, "docker", "exec", contract.Container(o), "test", "-f", contract.Credentials) == nil
}

// rcRunning is `docker exec claude-$1 pgrep -f "claude remote-control" >/dev/null`.
func (a *App) rcRunning(ctx context.Context, o string) bool {
	return a.passthrough(ctx, true, "docker", "exec", "claude-"+o, "pgrep", "-f", "claude remote-control") == nil
}

// rcBlocked is `docker exec … sh -c 'tail -n 2 …remote-control.log 2>/dev/null' | grep -q "blocked
// by organization policy"`: true only if docker succeeds (pipefail) and a line matches.
func (a *App) rcBlocked(ctx context.Context, o string) bool {
	out, err := a.capture(ctx, false, "docker", "exec", "claude-"+o, "sh", "-c",
		"tail -n 2 "+contract.RemoteControlLog+" 2>/dev/null")
	return err == nil && strings.Contains(out, contract.RemoteControlBlocked)
}

// rcURL is `docker exec … sh -c 'grep -ao "https://claude.ai/code?environment=[A-Za-z0-9_]*" … |
// tail -1' || true`: whatever it printed, even if it failed.
func (a *App) rcURL(ctx context.Context, o string) string {
	out, _ := a.capture(ctx, false, "docker", "exec", "claude-"+o, "sh", "-c",
		`grep -ao "https://claude.ai/code?environment=[A-Za-z0-9_]*" `+contract.RemoteControlLog+` 2>/dev/null | tail -1`)
	return out
}

// errBindTailscale is resolve_bind's die message.
var errBindTailscale = errors.New("BIND_ADDR=tailscale but tailscale is not up on this host")

// resolveBind is resolve_bind: BIND_ADDR, with "tailscale" meaning this host's current Tailscale
// IPv4 (`tailscale ip -4 2>/dev/null | head -1`), and 127.0.0.1 when unset. berth's own modes
// (#58, docs/networking.md): iface:<name> is that interface's IPv4 now (a WireGuard, ZeroTier,
// Netbird… interface), ip:<addr> is that address, and localhost is 127.0.0.1 (reached through an
// SSH tunnel: berth connect). Anything else is used as it is, as ccenv does.
func (a *App) resolveBind(ctx context.Context, o string) (string, error) {
	b := a.env(o, "BIND_ADDR")
	switch {
	case b == "tailscale":
		out, _ := a.capture(ctx, true, "tailscale", "ip", "-4")
		b, _, _ = strings.Cut(out, "\n")
		if b == "" {
			return "", errBindTailscale
		}
	case b == BindLocalhost:
		b = "127.0.0.1"
	case strings.HasPrefix(b, "iface:"):
		name := strings.TrimPrefix(b, "iface:")
		ip := a.ifaceIPv4(ctx, name)
		if ip == "" {
			return "", fmt.Errorf("BIND_ADDR=%s but %s has no IPv4 address on this host", b, name)
		}
		b = ip
	case strings.HasPrefix(b, "ip:"):
		b = strings.TrimPrefix(b, "ip:")
	}
	if b == "" {
		b = "127.0.0.1"
	}
	return b, nil
}

// BindLocalhost is the bind mode for orgs reached only through an SSH tunnel (berth connect).
const BindLocalhost = "localhost"

var ifaceName = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,15}$`)

// ifaceIPv4 is the first IPv4 address of a network interface ("" if none): ip on Linux, ifconfig
// elsewhere (macOS).
func (a *App) ifaceIPv4(ctx context.Context, name string) string {
	if !ifaceName.MatchString(name) {
		return ""
	}
	if out, err := a.capture(ctx, true, "ip", "-4", "-o", "addr", "show", "dev", name); err == nil {
		return firstInet(out, true)
	}
	out, _ := a.capture(ctx, true, "ifconfig", name)
	return firstInet(out, false)
}

// firstInet finds the first "inet <addr>" in ip's (addr/prefix) or ifconfig's output.
func firstInet(out string, withPrefix bool) string {
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "inet" {
			addr := f[i+1]
			if withPrefix {
				addr, _, _ = strings.Cut(addr, "/")
			}
			addr = strings.TrimPrefix(addr, "addr:") // older Linux ifconfig
			if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
				return addr
			}
		}
	}
	return ""
}

// CheckBind refuses a bind mode berth doesn't know, for the places that set one (config.yaml,
// host add --bind). org.env takes anything, as ccenv's does.
func CheckBind(b string) error {
	switch {
	case b == "", b == "tailscale", b == BindLocalhost, b == "0.0.0.0":
		return nil
	case strings.HasPrefix(b, "iface:") && ifaceName.MatchString(strings.TrimPrefix(b, "iface:")):
		return nil
	case strings.HasPrefix(b, "ip:") && net.ParseIP(strings.TrimPrefix(b, "ip:")) != nil:
		return nil
	case net.ParseIP(b) != nil:
		return nil
	}
	return fmt.Errorf("unknown bind mode %q (tailscale, iface:<name>, ip:<addr>, localhost, 0.0.0.0, or an address)", b)
}

// bindOrWarn is `$(resolve_bind …)` as ccenv uses it: inside a command substitution, where bash
// turns set -e off, so a failure prints its message and the caller carries on with "" (compose then
// falls back to its own default, and info shows an empty host).
func (a *App) bindOrWarn(ctx context.Context, o string) string {
	b, err := a.resolveBind(ctx, o)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: %v\n", Tool, err)
	}
	return b
}

// hostAddr is host_addr: the address remote clients should use ("" if resolve_bind failed).
func (a *App) hostAddr(ctx context.Context, o string) string {
	b := a.bindOrWarn(ctx, o)
	switch b {
	case "127.0.0.1":
		if a.env(o, "BIND_ADDR") == BindLocalhost || a.HostName != "" {
			return "127.0.0.1 (through an SSH tunnel: see below)"
		}
		return "127.0.0.1 (local only; set BIND_ADDR=tailscale)"
	case "0.0.0.0":
		for _, k := range []string{"BERTH_HOST", "CCENV_HOST"} { // CCENV_HOST: ccenv's name, still honoured
			if v := a.Getenv(k); v != "" {
				return v
			}
		}
		h, _ := a.capture(ctx, false, "hostname")
		return h
	}
	return b
}
