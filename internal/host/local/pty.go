package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// PTY is a command running on a pseudo-terminal of its own (#167): what the API gives a browser
// that signs an org in, where the command line would use the operator's terminal. berth doesn't
// reimplement a terminal: it allocates one, runs a command in it, and passes the bytes along.
type PTY struct {
	cmd  *exec.Cmd
	file *os.File
	once sync.Once
	done chan struct{}
	code int
	err  error
}

// StartPTY runs argv on a new pseudo-terminal of the size given, with env added to this
// process's environment. It ends with ctx, or with Close.
func StartPTY(ctx context.Context, argv, env []string, cols, rows int) (*PTY, error) {
	if len(argv) == 0 {
		return nil, errors.New("no command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the caller's command: berth itself, with fixed arguments
	cmd.Env = append(os.Environ(), env...)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: clampSize(cols, 80), Rows: clampSize(rows, 24)})
	if err != nil {
		return nil, err
	}
	p := &PTY{cmd: cmd, file: f, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit):
			p.code = exit.ExitCode()
		case err != nil:
			p.code, p.err = 1, err
		}
		close(p.done)
	}()
	return p, nil
}

func clampSize(n, def int) uint16 {
	if n < 2 || n > 1000 {
		n = def
	}
	return uint16(n) //nolint:gosec // between 2 and 1000
}

// Read is what the command prints. When the command has ended and everything it printed was
// read, the error is io.EOF's kind: a pty reports its closed far end as an input/output error.
func (p *PTY) Read(b []byte) (int, error) {
	n, err := p.file.Read(b)
	if err != nil && errors.Is(err, syscall.EIO) {
		err = os.ErrClosed
	}
	return n, err
}

// Write is what is typed to the command.
func (p *PTY) Write(b []byte) (int, error) { return p.file.Write(b) }

// Resize tells the command its terminal's size changed.
func (p *PTY) Resize(cols, rows int) error {
	return pty.Setsize(p.file, &pty.Winsize{Cols: clampSize(cols, 80), Rows: clampSize(rows, 24)})
}

// Wait is the command's exit code, once it has ended.
func (p *PTY) Wait() (int, error) {
	<-p.done
	return p.code, p.err
}

// Close ends the command if it still runs, and frees the terminal.
func (p *PTY) Close() error {
	p.once.Do(func() {
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		_ = p.file.Close()
	})
	<-p.done
	return nil
}
