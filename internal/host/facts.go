package host

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// ExecFacts derives Facts by running standard tools through an Execer, so the local and ssh hosts
// share one implementation (and one set of parsers to test).
type ExecFacts struct {
	Exec Execer
	// Docker, when set, adds ports published by running containers to PortsInUse.
	Docker Docker
}

func (f ExecFacts) Facts(ctx context.Context) (Facts, error) {
	var fa Facts
	var err error
	if fa.UID, err = f.number(ctx, "id", "-u"); err != nil {
		return fa, err
	}
	if fa.GID, err = f.number(ctx, "id", "-g"); err != nil {
		return fa, err
	}
	m, err := output(ctx, f.Exec, "uname", "-m")
	if err != nil {
		return fa, err
	}
	fa.Arch = dockerArch(strings.TrimSpace(m))
	// ccenv: `tailscale ip -4 2>/dev/null | head -1`. Not installed or not up both mean "".
	if out, err := output(ctx, f.Exec, "tailscale", "ip", "-4"); err == nil {
		fa.TailscaleIP = firstLine(out)
	}
	return fa, nil
}

func (f ExecFacts) PortsInUse(ctx context.Context) ([]int, error) {
	ports, err := f.listening(ctx)
	if err != nil {
		return nil, err
	}
	if f.Docker != nil {
		pub, err := NewEngine(f.Docker).publishedPorts(ctx)
		if err != nil {
			return nil, err
		}
		ports = append(ports, pub...)
	}
	slices.Sort(ports)
	return slices.Compact(ports), nil
}

// listening uses ss (Linux), falling back to netstat (macOS, minimal Linux).
func (f ExecFacts) listening(ctx context.Context) ([]int, error) {
	if out, err := output(ctx, f.Exec, "ss", "-Htln"); err == nil {
		return parseSS(out), nil
	}
	out, err := output(ctx, f.Exec, "netstat", "-an")
	if err != nil {
		return nil, fmt.Errorf("listing listening ports: neither ss nor netstat worked: %w", err)
	}
	return parseNetstat(out), nil
}

func (f ExecFacts) number(ctx context.Context, args ...string) (int, error) {
	out, err := output(ctx, f.Exec, args...)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("%s: unexpected output %q", strings.Join(args, " "), out)
	}
	return n, nil
}

// output runs args and returns stdout; a failure includes stderr.
func output(ctx context.Context, ex Execer, args ...string) (string, error) {
	var out, errb bytes.Buffer
	if err := ex.Run(ctx, Cmd{Args: args, Stdout: &out, Stderr: &errb}); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return "", fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return out.String(), nil
}

// IsExit reports whether err is a process that ran and exited non-zero (as opposed to one that
// could not be started or reached).
func IsExit(err error) bool {
	var ee *ExitError
	return errors.As(err, &ee)
}

func dockerArch(m string) string {
	switch m {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l":
		return "arm"
	}
	return m
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

// parseSS reads `ss -Htln`: "LISTEN 0 4096 127.0.0.1:2290 0.0.0.0:*" (local address is field 4).
func parseSS(out string) []int {
	var ports []int
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 4 {
			if p, ok := portOf(f[3], ':'); ok {
				ports = append(ports, p)
			}
		}
	}
	return ports
}

// parseNetstat reads `netstat -an` TCP LISTEN lines, in Linux ("0.0.0.0:22") or BSD/macOS ("*.22",
// "127.0.0.1.2290") form. The local address is field 4.
func parseNetstat(out string) []int {
	var ports []int
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || !strings.HasPrefix(f[0], "tcp") || f[len(f)-1] != "LISTEN" {
			continue
		}
		// Linux ends in ":port"; BSD ends in ".port", even for IPv6 ("::1.631").
		sep := byte('.')
		if i := strings.LastIndexByte(f[3], ':'); i >= 0 && !strings.Contains(f[3][i+1:], ".") {
			sep = ':'
		}
		if p, ok := portOf(f[3], sep); ok {
			ports = append(ports, p)
		}
	}
	return ports
}

func portOf(addr string, sep byte) (int, bool) {
	i := strings.LastIndexByte(addr, sep)
	if i < 0 {
		return 0, false
	}
	p, err := strconv.Atoi(addr[i+1:])
	return p, err == nil && p > 0 && p < 65536
}
