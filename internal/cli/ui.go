package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/ar4mirez/berth/internal/api"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/mcpsrv"
	"github.com/ar4mirez/berth/internal/version"
	"github.com/ar4mirez/berth/internal/web"
)

// uiCmd is `berth ui [--port N] [--no-open]` (#167): the web UI on this machine, for whoever ran it.
func uiCmd() *cobra.Command {
	var port int
	var noOpen bool
	c := reads(&cobra.Command{
		Use: "ui [--port N] [--no-open]", Short: "the web dashboard, in your browser", Args: cobra.NoArgs,
		Long: "Serves berth's web dashboard on this machine and opens it in your browser: every org and host\n" +
			"with its state, and a page per org with its connection sheet, firewall, repos, variable names,\n" +
			"packages, backups and log. It does what `berth tui` does, through the same operations.\n\n" +
			"It listens on 127.0.0.1 only, on a port of its own, and every request needs a token made for\n" +
			"this run: it is in the link berth prints, kept nowhere, and gone when berth ui ends. Anything\n" +
			"that restarts a container says what stops and runs only after the org's name is typed. With\n" +
			"--read-only it only reads. It runs until interrupted.\n\n" +
			"From another device, use `berth serve --listen` and a token (docs/guides/web.md).",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st := stateFrom(cmd.Context())
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return err
			}
			token := "berth_" + base64.RawURLEncoding.EncodeToString(raw)
			want := sha256.Sum256([]byte(token))
			opts := api.Options{Version: version.Version, ReadOnly: st.ReadOnly, NewApp: appMaker(st), UI: web.Handler()}
			opts.Caller = func(req *http.Request) (mcpsrv.Caller, error) {
				h := req.Header.Get("Authorization")
				got := sha256.Sum256([]byte(strings.TrimPrefix(h, "Bearer ")))
				if !strings.HasPrefix(h, "Bearer ") || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
					return mcpsrv.Caller{}, api.ErrUnauthorized
				}
				return mcpsrv.Caller{Writes: true, Restarts: true}, nil
			}
			// 127.0.0.1 and nothing else: without TLS, this is for the machine it runs on.
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				return err
			}
			srv := &http.Server{Handler: api.Handler(opts), ReadHeaderTimeout: 10 * time.Second}
			errs := make(chan error, 1)
			go func() { errs <- srv.Serve(l) }()
			link := fmt.Sprintf("http://%s/#token=%s", l.Addr(), token)
			mode := "reads and writes"
			if st.ReadOnly {
				mode = "read-only"
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "berth ui: serving on %s (%s) until interrupted. The link holds this run's token:\n", l.Addr(), mode)
			// The link goes to stdout alone, so a script can capture it.
			fmt.Fprintln(cmd.OutOrStdout(), link)
			if !noOpen {
				if remove, err := openBrowser(cmd.Context(), appFor(cmd).Operator, link); err == nil {
					defer remove()
				}
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			select {
			case <-ctx.Done():
			case err = <-errs:
			}
			end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(end)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	})
	c.Flags().IntVar(&port, "port", 0, "the port on 127.0.0.1 to listen on (default: any free one)")
	c.Flags().BoolVar(&noOpen, "no-open", false, "print the link and don't open a browser")
	return c
}

// openBrowser opens link in the user's browser, when there is a desktop to open one on. The link
// holds a token, and a command's arguments are visible to every user of the machine: the browser
// is given a file only its owner can read, which sends it on. remove deletes that file.
func openBrowser(ctx context.Context, op *host.Host, link string) (remove func(), err error) {
	var opener string
	switch {
	case runtime.GOOS == "darwin":
		opener = "open"
	case os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != "":
		opener = "xdg-open"
	default:
		return nil, errors.New("no desktop")
	}
	if _, err := exec.LookPath(opener); err != nil {
		return nil, err
	}
	dir := filepath.Dir(api.SocketPath(os.Getenv))
	if err := op.FS.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := make([]byte, 8)
	if _, err := rand.Read(name); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "berth-ui-"+hex.EncodeToString(name)+".html")
	page := fmt.Sprintf("<!doctype html>\n<meta charset=\"utf-8\">\n<title>berth</title>\n<meta http-equiv=\"refresh\" content=\"0;url=%[1]s\">\n<a href=\"%[1]s\">berth</a>\n", link)
	if err := op.FS.WriteFileAtomic(file, []byte(page), 0o600); err != nil {
		return nil, err
	}
	// The opener returns once the browser has the file; it isn't waited for, and its output isn't berth's.
	go func() {
		_ = op.Exec.Run(ctx, host.Cmd{Args: []string{opener, file}, Stdout: io.Discard, Stderr: io.Discard})
	}()
	return func() { _ = op.FS.Remove(file) }, nil
}
