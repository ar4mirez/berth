package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/apiclient"
	"github.com/ar4mirez/berth/internal/app"
	"github.com/ar4mirez/berth/internal/host"
)

// A registered host that runs its own `berth serve` is asked for whole operations (#148): berth
// forwards the host's API socket over the ssh connection it already has, and the host's berth
// runs the command there (POST /v1/cli), in one exchange where the ssh path takes one per file
// read and per docker command. No port is opened on the host.
//
// Only operations that are the same wherever they run go that way: the ones below touch the org
// and its container and nothing else. What depends on this machine keeps the ssh path: the
// lifecycle (the active-host lease, the host guard), info and ls (the registry, the tunnel),
// backups (your key). A host with no `berth serve` is driven over ssh as before.

// hostAPIOps are the operations (name and subcommand, as internal/ops has them) sent to a host's
// own berth. Each must be one the API serves (daemonCan).
var hostAPIOps = map[string]bool{
	"fw ": true, "fw show": true, "fw presets": true, "fw test": true, "fw allow": true, "fw deny": true,
	"repo ls": true, "repo add": true, "repo rm": true,
	"env ": true, "env ls": true, "pkg ": true, "pkg ls": true,
	"remote ": true, "remote status": true,
}

// hostSocket finds the socket of the `berth serve` on b's host, as the user berth logs in as: ""
// when there is none.
func hostSocket(ctx context.Context, b *app.App) string {
	var out bytes.Buffer
	// The same two places `berth serve` chooses from (api.SocketPath).
	const find = `s="${XDG_RUNTIME_DIR:-/nonexistent}/berth.sock"; [ -S "$s" ] || s="${XDG_CONFIG_HOME:-$HOME/.config}/berth/berth.sock"; [ -S "$s" ] && printf %s "$s"`
	if err := b.Host.Exec.Run(ctx, host.Cmd{Args: []string{"sh", "-c", find}, Stdout: &out, Stderr: io.Discard}); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// throughHostAPI runs the command on b's host through its own berth, when the host has one
// serving and the operation is one that may go. args are the flat command's, with the bare org.
// handled is false when it didn't go that way, and the caller carries on over ssh.
func throughHostAPI(cmd *cobra.Command, b *app.App, args []string) (handled bool, err error) {
	if b.HostName == "" || b.Host.Unix == nil || os.Getenv("BERTH_HOST_API") == "off" {
		return false, nil
	}
	name := cmd.Name()
	if n := cmd.Annotations[opKey]; n != "" {
		name = n
	}
	if !hostAPIOps[name+" "+subOf(name, args)] {
		return false, nil
	}
	ctx := cmd.Context()
	sock := hostSocket(ctx, b)
	if sock == "" {
		return false, nil
	}
	client := &apiclient.Client{Base: "http://berth", HTTP: &http.Client{Transport: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return b.Host.Unix(ctx, sock) },
		DisableKeepAlives: true, // one command, one ssh channel
	}}}
	// A socket nobody answers on (a server that died) isn't an error here: ssh still works.
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, perr := client.Info(probe)
	cancel()
	if perr != nil {
		return false, nil
	}
	code, err := client.CLI(ctx, append([]string{name}, args...), outputFrom(ctx), cmd.OutOrStdout(), cmd.ErrOrStderr())
	if err != nil {
		return true, err
	}
	if code != 0 {
		return true, &ExitError{Code: code}
	}
	return true, nil
}
