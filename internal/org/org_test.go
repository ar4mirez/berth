package org

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/ar4mirez/berth/internal/host/local"
)

// All fixtures are synthetic: no real org.env ever lands in testdata or a test.

// writeOrg creates <orgs>/<name>/org.env (mode 0600, as ccenv init makes it).
func writeOrg(t *testing.T, orgs, name, content string) string {
	t.Helper()
	dir := filepath.Join(orgs, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "org.env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLookup(t *testing.T) {
	tests := []struct {
		name, env, key, want string
		found                bool
	}{
		{"simple", "SSH_PORT=2201\n", "SSH_PORT", "2201", true},
		{"last duplicate wins", "TTYD_PORT=7701\nMEM_LIMIT=8g\nTTYD_PORT=7702\n", "TTYD_PORT", "7702", true},
		{"missing key", "SSH_PORT=2201\n", "TTYD_PORT", "", false},
		{"missing in empty file", "", "SSH_PORT", "", false},
		{"empty value is found", "CLAUDE_CODE_OAUTH_TOKEN=\n", "CLAUDE_CODE_OAUTH_TOKEN", "", true},
		{"equals kept", "GH_TOKEN=abc=def==\n", "GH_TOKEN", "abc=def==", true},
		{"no unquoting", `GIT_USER_NAME="Ada  O'Brien" $HOME` + "\n", "GIT_USER_NAME", `"Ada  O'Brien" $HOME`, true},
		{"single quotes and backslash", `A='x\ty' \n` + "\n", "A", `'x\ty' \n`, true},
		{"spaces kept", "GIT_USER_NAME=  Test User  \n", "GIT_USER_NAME", "  Test User  ", true},
		{"no trailing newline", "A=1\nGIT_USER_EMAIL=t@example.com", "GIT_USER_EMAIL", "t@example.com", true},
		{"CRLF keeps \\r", "SSH_PORT=2201\r\nB=2\r\n", "SSH_PORT", "2201\r", true},
		{"longer key is not a match", "SSH_PORT_X=9\n", "SSH_PORT", "", false},
		{"shorter key is not a match", "SSH_PORT=9\n", "SSH_POR", "", false},
		{"comments and indents ignored", "# SSH_PORT=1\n SSH_PORT=2\nexport SSH_PORT=3\n", "SSH_PORT", "", false},
		{"lowercase is another key", "ssh_port=1\n", "SSH_PORT", "", false},
		{"unicode and tabs", "GIT_USER_NAME=Zoë\tÅström\n", "GIT_USER_NAME", "Zoë\tÅström", true},
	}
	orgs := t.TempDir()
	lg := newLegacy(t, orgs)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := Lookup([]byte(tt.env), tt.key)
			if got != tt.want || found != tt.found {
				t.Errorf("Lookup = %q, %v; want %q, %v", got, found, tt.want, tt.found)
			}
			if lg == nil {
				return
			}
			writeOrg(t, orgs, "t-env", tt.env)
			out, _ := lg.run(t, `envval t-env "$K" || true`, "K="+tt.key)
			want := ""
			if tt.found {
				want = tt.want + "\n" // cut ends its output with a newline
			}
			if out != want {
				t.Errorf("legacy envval printed %q; Go says %q (found=%v)", out, got, found)
			}
		})
	}
}

func TestSet(t *testing.T) {
	tests := []struct {
		name, env, key, value, want string
	}{
		{"replace", "SSH_PORT=2201\nTTYD_PORT=7701\n", "SSH_PORT", "2202", "SSH_PORT=2202\nTTYD_PORT=7701\n"},
		{"append", "SSH_PORT=2201\n", "SHARED_ACCOUNT", "1", "SSH_PORT=2201\nSHARED_ACCOUNT=1\n"},
		{"append after missing newline", "SSH_PORT=2201", "REPO_POLICY", "warn", "SSH_PORT=2201\nREPO_POLICY=warn\n"},
		{"missing newline gets one", "A=1\nSSH_PORT=2201", "SSH_PORT", "2", "A=1\nSSH_PORT=2\n"},
		{"every duplicate replaced", "TTYD_PORT=1\nMEM_LIMIT=8g\nTTYD_PORT=2\n", "TTYD_PORT", "7", "TTYD_PORT=7\nMEM_LIMIT=8g\nTTYD_PORT=7\n"},
		{"clear a value", "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-fake\n", "CLAUDE_CODE_OAUTH_TOKEN", "", "CLAUDE_CODE_OAUTH_TOKEN=\n"},
		{"value written raw", "A=1\n", "GIT_USER_NAME", `a=b $HOME "q" 'r' \t\\ ünï`, "A=1\nGIT_USER_NAME=a=b $HOME \"q\" 'r' \\t\\\\ ünï\n"},
		{"empty file", "", "SSH_PORT", "2201", "SSH_PORT=2201\n"},
		{"CRLF: replaced line loses \\r, others keep it", "SSH_PORT=1\r\nB=2\r\n", "SSH_PORT", "5", "SSH_PORT=5\nB=2\r\n"},
		{"comments and blank lines kept", "# ---- Access\n\nSSH_PORT=1\n\n", "SSH_PORT", "2", "# ---- Access\n\nSSH_PORT=2\n\n"},
		{"longer key untouched", "SSH_PORT_X=9\n", "SSH_PORT", "2", "SSH_PORT_X=9\nSSH_PORT=2\n"},
	}
	orgs := t.TempDir()
	lg := newLegacy(t, orgs)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(Set([]byte(tt.env), tt.key, tt.value)); got != tt.want {
				t.Errorf("Set =\n%q\nwant\n%q", got, tt.want)
			}
			if lg == nil {
				return
			}
			p := writeOrg(t, orgs, "t-env", tt.env)
			lg.run(t, `setval t-env "$K" "$V"`, "K="+tt.key, "V="+tt.value)
			if b, _ := os.ReadFile(p); string(b) != tt.want {
				t.Errorf("legacy setval wrote\n%q\nGo writes\n%q", b, tt.want)
			}
		})
	}
}

func inodeMode(t *testing.T, p string) (uint64, fs.FileMode) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Sys().(*syscall.Stat_t).Ino, fi.Mode().Perm()
}

func TestOrgsGetSet(t *testing.T) {
	orgs := t.TempDir()
	o := Orgs{FS: local.FS{}, Dir: orgs}
	p := writeOrg(t, orgs, "acme", "SSH_PORT=2201\nTTYD_PORT=7701\n")
	ino, mode := inodeMode(t, p)

	if err := o.Set("acme", "SSH_PORT", "2290"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := o.Get("acme", "SSH_PORT"); err != nil || !ok || v != "2290" {
		t.Errorf("Get after Set: %q %v %v", v, ok, err)
	}
	if gotIno, gotMode := inodeMode(t, p); gotIno != ino || gotMode != mode || mode != 0o600 {
		t.Errorf("inode/mode %d/%o -> %d/%o; setval keeps both (and init makes it 600)", ino, mode, gotIno, gotMode)
	}
	if _, ok, err := o.Get("acme", "REMOTE_CONTROL"); err != nil || ok {
		t.Errorf("missing key: ok=%v err=%v", ok, err)
	}

	// Like setval, Set never creates org.env; Get on a missing org is an error, not "".
	if err := o.Set("globex", "SSH_PORT", "1"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Set on a missing org: %v", err)
	}
	if _, err := os.Stat(filepath.Join(orgs, "globex")); !os.IsNotExist(err) {
		t.Error("Set created files for a missing org")
	}
	if _, _, err := o.Get("globex", "SSH_PORT"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Get on a missing org: %v", err)
	}

	for _, bad := range []string{"", "SSH PORT", "A.B", "^SSH", "1X"} {
		if err := o.Set("acme", bad, "x"); err == nil {
			t.Errorf("key %q accepted", bad)
		}
	}
}

func TestNextPort(t *testing.T) {
	type org struct{ name, env string }
	tests := []struct {
		name string
		orgs []org
		key  string
		base int
		want int
	}{
		{"no orgs", nil, "SSH_PORT", 2201, 2201},
		{"max plus one", []org{{"acme", "SSH_PORT=2201\n"}, {"globex", "SSH_PORT=2203\n"}}, "SSH_PORT", 2201, 2204},
		{"never below base", []org{{"acme", "TTYD_PORT=100\n"}}, "TTYD_PORT", 7701, 7701},
		{"key missing in a file", []org{{"acme", "SSH_PORT=2201\n"}, {"globex", "TTYD_PORT=9999\n"}}, "SSH_PORT", 2201, 2202},
		{"file with duplicate key skipped", []org{{"acme", "SSH_PORT=2202\n"}, {"globex", "SSH_PORT=2201\nSSH_PORT=2250\n"}}, "SSH_PORT", 2201, 2203},
		{"empty duplicate is not a duplicate", []org{{"acme", "SSH_PORT=\nSSH_PORT=2240\n"}}, "SSH_PORT", 2201, 2241},
		{"non-numeric skipped", []org{{"acme", "SSH_PORT=2201\n"}, {"initech", "SSH_PORT=abc\n"}}, "SSH_PORT", 2201, 2202},
		{"cut -f2 stops at the next =", []org{{"acme", "SSH_PORT=2300=x\n"}}, "SSH_PORT", 2201, 2301},
		{"spaces and tabs around the number", []org{{"acme", "SSH_PORT= 2210\t\n"}}, "SSH_PORT", 2201, 2211},
		{"CRLF value skipped", []org{{"acme", "SSH_PORT=2220\r\n"}, {"globex", "SSH_PORT=2201\n"}}, "SSH_PORT", 2201, 2202},
		{"explicit plus sign", []org{{"acme", "SSH_PORT=+2230\n"}}, "SSH_PORT", 2201, 2231},
		{"restore zeroes itself first", []org{{"acme", "SSH_PORT=2201\n"}, {"t-restored", "SSH_PORT=0\n"}}, "SSH_PORT", 2201, 2202},
		{"hidden staging dir ignored", []org{{"acme", "SSH_PORT=2201\n"}, {".restore-Ab12Cd", "SSH_PORT=9999\n"}}, "SSH_PORT", 2201, 2202},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgs := filepath.Join(t.TempDir(), "orgs")
			if err := os.MkdirAll(orgs, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, o := range tt.orgs {
				writeOrg(t, orgs, o.name, o.env)
			}
			// Noise the glob must skip: a dir without org.env, and a plain file.
			if err := os.MkdirAll(filepath.Join(orgs, "t-empty"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(orgs, "notes.txt"), []byte("SSH_PORT=9999\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Orgs{FS: local.FS{}, Dir: orgs}.NextPort(tt.key, tt.base)
			if err != nil || got != tt.want {
				t.Errorf("NextPort = %d, %v; want %d", got, err, tt.want)
			}
			if lg := newLegacy(t, orgs); lg != nil {
				// Called the way ccenv calls it: inside $( ), where bash clears set -e.
				out, _ := lg.run(t, `echo "$(next_port "$K" "$B")"`, "K="+tt.key, "B="+strconv.Itoa(tt.base))
				if out != strconv.Itoa(got)+"\n" {
					t.Errorf("legacy next_port printed %q; Go says %d", out, got)
				}
			}
		})
	}
}

func TestNextPortFollowsSymlinks(t *testing.T) {
	root := t.TempDir()
	orgs := filepath.Join(root, "orgs")
	writeOrg(t, root, "elsewhere", "SSH_PORT=2260\n")
	if err := os.MkdirAll(orgs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(orgs, "t-link")); err != nil {
		t.Fatal(err)
	}
	got, err := Orgs{FS: local.FS{}, Dir: orgs}.NextPort("SSH_PORT", 2201)
	if err != nil || got != 2261 {
		t.Errorf("NextPort = %d, %v; want 2261 (the glob follows a symlinked org dir)", got, err)
	}
	if lg := newLegacy(t, orgs); lg != nil {
		if out, _ := lg.run(t, `echo "$(next_port SSH_PORT 2201)"`); out != "2261\n" {
			t.Errorf("legacy next_port printed %q", out)
		}
	}
}

func TestNextPortMissingOrgsDir(t *testing.T) {
	got, err := Orgs{FS: local.FS{}, Dir: filepath.Join(t.TempDir(), "orgs")}.NextPort("SSH_PORT", 2201)
	if err != nil || got != 2201 {
		t.Errorf("got %d, %v; want the base", got, err)
	}
}

func TestBashInt(t *testing.T) {
	ok := map[string]int64{"2201": 2201, " 2201": 2201, "\t2201 \t": 2201, "\n2201": 2201, "+5": 5, "-5": -5, "007": 7}
	for in, want := range ok {
		if got, valid := bashInt(in); !valid || got != want {
			t.Errorf("bashInt(%q) = %d, %v; want %d", in, got, valid, want)
		}
	}
	for _, in := range []string{"", " ", "abc", "22 01", "2201\r", "2201\n2202", "0x10", "99999999999999999999", "+", "1e3"} {
		if _, valid := bashInt(in); valid {
			t.Errorf("bashInt(%q) accepted", in)
		}
	}
}
