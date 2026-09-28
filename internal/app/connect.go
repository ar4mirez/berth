package app

import (
	"context"
	"fmt"
	"net"

	"github.com/ar4mirez/berth/internal/host"
)

// Connect is `berth connect <org>[@host]` (#58): an SSH tunnel from this machine to an org on a
// registered host, so its SSH and browser terminal answer on 127.0.0.1 here, whatever the org binds
// to there (localhost, a VPN interface, Tailscale). It runs until Ctrl-C. For an org on this machine
// there's nothing to tunnel: it says where to connect.
func (a *App) Connect(ctx context.Context, o string) error {
	if err := a.needOrg(o); err != nil {
		return err
	}
	sp, tp := a.env(o, "SSH_PORT"), a.env(o, "TTYD_PORT")
	if portOf(sp) == nil || portOf(tp) == nil {
		return fmt.Errorf("%s has no SSH_PORT/TTYD_PORT in its org.env", o)
	}
	bind, err := a.resolveBind(ctx, o)
	if err != nil {
		return err
	}
	if bind == "0.0.0.0" {
		bind = "127.0.0.1" // listening everywhere: the host's loopback is one of them
	}
	if a.HostName == "" {
		fmt.Fprintf(a.Stdout, "%s runs on this machine: ssh -p %s node@%s, or http://%s:%s (password: %s password %s)\n",
			o, sp, bind, bind, tp, Tool, o)
		if b := a.env(o, "BIND_ADDR"); b == BindLocalhost || b == "127.0.0.1" || b == "" {
			fmt.Fprintf(a.Stdout, "From another device: %s info %s shows the ssh -L command.\n", Tool, o)
		}
		return nil
	}
	e, err := a.base().registered(a.HostName)
	if err != nil {
		return err
	}
	h, p, err := net.SplitHostPort(e.Addr)
	if err != nil {
		return err
	}
	fwd := func(port string) string { return port + ":" + bind + ":" + port }
	args := []string{"ssh", "-N", "-p", p, "-i", e.Key,
		"-o", "IdentitiesOnly=yes", "-o", "ExitOnForwardFailure=yes",
		"-o", "UserKnownHostsFile=" + a.hostPaths().KnownHosts(), "-o", "StrictHostKeyChecking=yes",
		"-L", "127.0.0.1:" + fwd(sp), "-L", "127.0.0.1:" + fwd(tp), e.User + "@" + h}
	fmt.Fprintf(a.Stdout, "Tunnel to %s on %s (Ctrl-C closes it):\n  SSH                ssh -p %s node@127.0.0.1\n  Browser terminal   http://127.0.0.1:%s   (password: %s password %s@%s)\n",
		o, a.HostName, sp, tp, Tool, o, a.HostName)
	return a.Operator.Exec.Run(ctx, host.Cmd{Args: args, TTY: true})
}
