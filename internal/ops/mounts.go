package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// Socket mounts (#216): a Unix socket on the org's host, given to its container under
// /run/berth-mounts/. The list is orgs/<org>/mounts.json, a setting like packages.txt: a change
// applies at the org's next up or restart, and backups keep it (never the socket). What is bound
// is the socket's directory, not its inode, so a daemon that makes its socket again is seen
// without a restart. That directory is the socket's own: berth's user owns it, nobody else may
// write to it, and it holds no other socket and no link, checked when the mount is added and again
// at every start.

// MountsRoot is where every socket mount is in the container.
const MountsRoot = "/run/berth-mounts"

// MountsFile is an org's list of socket mounts.
func MountsFile(s System, org string) string { return path.Join(s.OrgsDir(), org, "mounts.json") }

// Mount is one socket mount: its name, the socket on the host, and where it is in the container.
type Mount struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
	// Mode is rw: a socket needs it, and nothing else is taken.
	Mode string `json:"mode"`
	// Applied is whether the running container has it: false until the container is recreated.
	Applied bool `json:"applied"`
}

// Mounts is `mount <org> ls` (berth.mounts/v1).
type Mounts struct {
	Schema string  `json:"schema"`
	Org    string  `json:"org"`
	Mounts []Mount `json:"mounts"`
	// Pending is whether the running container's mounts differ from the list: a change waits for
	// the org's next restart.
	Pending bool `json:"pending"`
}

// MountDoc is one mount after `mount <org> add` (berth.mount/v1).
type MountDoc struct {
	Schema  string `json:"schema"`
	Org     string `json:"org"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Mode    string `json:"mode"`
	Applied bool   `json:"applied"`
}

type mountsFile struct {
	Mounts []Mount `json:"mounts"`
}

// Usage is a usage error: the arguments don't fit (400 through the API).
func Usage(format string, args ...any) *Error { return fail(KindUsage, "", format, args...) }

// State is a state error: the org, or its host, isn't in a state for this (409 through the API).
func State(format string, args ...any) *Error { return fail(KindState, "", format, args...) }

var mountName = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// ReadMounts is an org's list as saved, in name order ([] when it has none).
func ReadMounts(s System, org string) ([]Mount, error) {
	b, err := s.ReadFile(MountsFile(s, org))
	if errors.Is(err, fs.ErrNotExist) {
		return []Mount{}, nil
	}
	if err != nil {
		return nil, err
	}
	var f mountsFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", MountsFile(s, org), err)
	}
	out := make([]Mount, 0, len(f.Mounts))
	for _, m := range f.Mounts {
		m.Applied = false
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b Mount) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// EncodeMounts is the file ReadMounts reads: the mounts in name order, without whether each is
// applied, which is the container's to say.
func EncodeMounts(ms []Mount) []byte {
	type entry struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Target string `json:"target"`
		Mode   string `json:"mode"`
	}
	f := struct {
		Mounts []entry `json:"mounts"`
	}{Mounts: make([]entry, 0, len(ms))}
	for _, m := range ms {
		f.Mounts = append(f.Mounts, entry{m.Name, m.Source, m.Target, m.Mode})
	}
	slices.SortFunc(f.Mounts, func(a, b entry) int { return strings.Compare(a.Name, b.Name) })
	b, _ := json.MarshalIndent(f, "", "  ")
	return append(b, '\n')
}

// Binding is what the container binds for m: the socket's directory, at its target's.
func (m Mount) Binding() (source, target string) { return path.Dir(m.Source), path.Dir(m.Target) }

// CheckMount checks what a mount is, without the host: its name, a socket's absolute path, a
// target under MountsRoot with the socket's name, and mode rw. others are the org's other mounts:
// no two share a directory in the container, or sit one inside the other.
func CheckMount(m Mount, others []Mount) error {
	if !mountName.MatchString(m.Name) {
		return Usage("the mount's name %q: lowercase letters, digits and dashes, up to 32", m.Name)
	}
	for what, p := range map[string]string{"source": m.Source, "target": m.Target} {
		if !path.IsAbs(p) || path.Clean(p) != p {
			return Usage("%s %q: an absolute path, written plainly (no .., no trailing /)", what, p)
		}
		if strings.ContainsAny(p, ":,\n\r\t\\\"'") {
			return Usage("%s %q: a path with no colon, comma, quote, backslash or control character", what, p)
		}
	}
	if m.Mode != "rw" {
		return Usage("mode %q: a socket is mounted rw, and nothing else is taken", m.Mode)
	}
	dir := path.Dir(m.Target)
	if !strings.HasPrefix(dir, MountsRoot+"/") {
		return Usage("target %q: a socket in a directory of its own under %s/, as in %s/agent/agent.sock", m.Target, MountsRoot, MountsRoot)
	}
	if path.Base(m.Target) != path.Base(m.Source) {
		return Usage("target %q: the socket keeps its name in the container (%s), since its directory is what is mounted", m.Target, path.Base(m.Source))
	}
	if path.Dir(m.Source) == "/" {
		return Usage("source %q: a socket in a directory of its own, not in /", m.Source)
	}
	for _, o := range others {
		if o.Name == m.Name {
			continue
		}
		od := path.Dir(o.Target)
		if od == dir || strings.HasPrefix(dir, od+"/") || strings.HasPrefix(od, dir+"/") {
			return Usage("target %q: %s already uses %s in the container", m.Target, o.Name, od)
		}
	}
	return nil
}

// sharedDirs can never be a socket's own directory: other things keep their sockets there.
var sharedDirs = []string{"/", "/tmp", "/var/tmp", "/run", "/var/run", "/dev", "/dev/shm", "/run/user", "/var/lib", "/home"}

// CheckSocket checks a mount's socket on the org's host: a socket (not a link to one), owned by
// berth's user, in a directory berth's user owns, that nobody else may write to, that is no shared
// place (/tmp, the user's runtime directory or home), and whose whole tree holds no other socket, no
// link, and no directory berth's user doesn't own or others may write to, at any depth: the
// socket's directory, with everything under it, is all the container gets. It needs GNU stat and
// find (a Linux host).
func CheckSocket(ctx context.Context, s System, source string) error {
	uid, err := s.Capture(ctx, true, "id", "-u")
	if err != nil {
		return fmt.Errorf("can't tell berth's user here: %w", err)
	}
	st := func(p string) (kind, owner string, mode int64, err error) {
		out, err := s.Capture(ctx, true, "stat", "-c", "%F|%u|%a", "--", p)
		if err != nil {
			return "", "", 0, err
		}
		f := strings.Split(strings.TrimSpace(out), "|")
		if len(f) != 3 {
			return "", "", 0, fmt.Errorf("stat %s: %q", p, out)
		}
		mode, err = strconv.ParseInt(f[2], 8, 32)
		return f[0], f[1], mode, err
	}
	kind, owner, _, err := st(source)
	if err != nil {
		return Usage("source %s: no such socket here (socket mounts need a Linux host)", source)
	}
	switch {
	case kind != "socket":
		return Usage("source %s: a %s, not a socket: only a Unix socket is mounted", source, kind)
	case owner != uid:
		return Usage("source %s: owned by uid %s, not by berth's user (uid %s)", source, owner, uid)
	}
	dir := path.Dir(source)
	home, _ := s.Capture(ctx, true, "sh", "-c", `printf '%s' "$HOME"`)
	if slices.Contains(sharedDirs, dir) || dir == "/run/user/"+uid || (home != "" && dir == path.Clean(home)) {
		return Usage("source %s: %s is shared with other programs' sockets; give the socket a directory of its own", source, dir)
	}
	kind, owner, mode, err := st(dir)
	switch {
	case err != nil:
		return State("source %s: can't read its directory: %v", source, err)
	case kind != "directory":
		return Usage("source %s: its directory %s is a %s, not a directory", source, dir, kind)
	case owner != uid:
		return Usage("source %s: its directory %s is owned by uid %s, not by berth's user (uid %s)", source, dir, owner, uid)
	case mode&0o022 != 0:
		return Usage("source %s: others may write to its directory %s (mode %o)", source, dir, mode)
	}
	list, err := s.Capture(ctx, true, "find", dir, "-mindepth", "1", "(", "-type", "s", "-o", "-type", "l",
		"-o", "-type", "d", "(", "-perm", "/022", "-o", "!", "-uid", uid, ")", ")", "-printf", `%y %p\n`)
	if err != nil {
		return State("source %s: can't list its directory: %v", source, err)
	}
	for _, line := range strings.Split(list, "\n") {
		t, p, _ := strings.Cut(strings.TrimSpace(line), " ")
		switch {
		case p == "" || p == source:
		case t == "d":
			return Usage("source %s: others may write to %s, or berth's user doesn't own it, and the container would get it too", source, p)
		default:
			return Usage("source %s: its directory also holds %s, which the container would get too; give the socket a directory of its own", source, p)
		}
	}
	return nil
}

// containerBinds are the running container's bind mounts, by where they are in it.
func containerBinds(ctx context.Context, s System, org string) map[string]string {
	out, err := s.Capture(ctx, true, "docker", "inspect", "-f", "{{json .Mounts}}", contract.Container(org))
	if err != nil {
		return nil
	}
	var ms []struct{ Type, Source, Destination string }
	if json.Unmarshal([]byte(out), &ms) != nil {
		return nil
	}
	binds := map[string]string{}
	for _, m := range ms {
		if m.Type == "bind" || m.Type == "" {
			binds[m.Destination] = m.Source
		}
	}
	return binds
}

// GetMounts is `mount <org> ls`.
func GetMounts(ctx context.Context, s System, org string) (Mounts, error) {
	out := Mounts{Schema: "berth.mounts/v1", Org: org, Mounts: []Mount{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	ms, err := ReadMounts(s, org)
	if err != nil {
		return out, err
	}
	if !Running(ctx, s, org) {
		out.Mounts = ms
		return out, nil
	}
	binds := containerBinds(ctx, s, org)
	want := map[string]bool{}
	for i, m := range ms {
		src, dst := m.Binding()
		want[dst] = true
		ms[i].Applied = binds != nil && binds[dst] == src
		if !ms[i].Applied {
			out.Pending = true
		}
	}
	for dst := range binds {
		if strings.HasPrefix(dst, MountsRoot+"/") && !want[dst] {
			out.Pending = true // removed from the list, still in the container
		}
	}
	out.Mounts = ms
	return out, nil
}
