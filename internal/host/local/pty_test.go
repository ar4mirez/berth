package local

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// TestPTY: a command on a pseudo-terminal sees a terminal, prints to it, reads what is typed, and
// ends with its exit code; Close ends one that is still running.
func TestPTY(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := StartPTY(ctx, []string{"sh", "-c", `test -t 0 && printf 'tty %s\n' "$BERTH_T"; stty size; printf 'code: '; read x; echo "got:$x"; exit 3`}, []string{"BERTH_T=yes"}, 132, 40)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	buf := make([]byte, 4096)
	typed := false
	for {
		n, err := p.Read(buf)
		out.Write(buf[:n])
		if !typed && strings.Contains(out.String(), "code: ") {
			typed = true
			if _, err := io.WriteString(p, "abc-123\n"); err != nil {
				t.Fatal(err)
			}
		}
		if err != nil {
			break
		}
	}
	code, err := p.Wait()
	got := strings.ReplaceAll(out.String(), "\r", "")
	if err != nil || code != 3 || !strings.Contains(got, "tty yes\n") || !strings.Contains(got, "40 132\n") || !strings.Contains(got, "got:abc-123\n") {
		t.Errorf("exit %d (%v), output %q", code, err, got)
	}
	_ = p.Close()

	// One that never ends by itself.
	p, err = StartPTY(ctx, []string{"sh", "-c", "echo up; sleep 300"}, nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := p.Read(buf); !strings.Contains(string(buf[:n]), "up") {
		t.Errorf("read %q", buf[:n])
	}
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close didn't end the command")
	}
	if _, err := StartPTY(ctx, nil, nil, 0, 0); err == nil {
		t.Error("no command started something")
	}
}
