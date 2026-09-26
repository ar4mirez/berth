//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/assets"
)

// The backup matrix (docs/plan.md, phase 4): {legacy, berth} creator × {legacy, berth} restorer ×
// {none, gpg, age, age-ssh} × {file, stdin}. Every restore must extract the same tree, owners and
// modes included: sshd/ stays 0:0 with its key 0600, and regenerable data is skipped.
//
// The engine is image/archive.sh, as in production, run in a slim image with only what it needs
// (bash, age, gpg, zstd, jq, GNU tar), tagged as both tools' image: this tests the backup format and
// the two CLIs, not the claude-env image.

const engineDockerfile = `FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends age gnupg zstd jq tar ca-certificates && rm -rf /var/lib/apt/lists/*
`

const bkOrg = "t-bk"

type matrixEnv struct {
	root, berth, legacy string
	home                string   // HOME for both tools
	env                 []string // common environment
	src                 string   // the state root holding t-bk
}

func TestBackupMatrix(t *testing.T) {
	root, err := filepath.Abs("../..")
	mustDo(t, err)
	berth := os.Getenv("BERTH_BIN")
	if berth == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	set, err := assets.Embedded(t.TempDir())
	mustDo(t, err)
	build := exec.Command("docker", "build", "-t", "claude-env", "-t", set.Tag, "-")
	build.Stdin = strings.NewReader(engineDockerfile)
	run(t, build)

	m := &matrixEnv{root: root, berth: berth, legacy: filepath.Join(root, "legacy", "ccenv"), home: t.TempDir()}
	m.env = append(os.Environ(), "HOME="+m.home, "XDG_CONFIG_HOME=", "CCENV_BACKUP_PASSPHRASE=", "CCENV_BACKUP_RECIPIENTS=", "CCENV_BACKUP_KEY=")
	m.src = m.newState(t)
	m.fixture(t)

	// Keys: an age key, and an ssh key (age encrypts to ssh-ed25519 recipients too).
	ageKey := filepath.Join(m.home, "age.key")
	mustDo(t, os.WriteFile(ageKey, []byte(docker(t, "run", "--rm", "--entrypoint", "age-keygen", "claude-env")), 0o600))
	agePub := strings.TrimSpace(docker(t, "run", "--rm", "-v", ageKey+":/k:ro", "--entrypoint", "age-keygen", "claude-env", "-y", "/k"))
	sshKey := filepath.Join(m.home, "id_ed25519")
	run(t, exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "matrix", "-f", sshKey))
	sshPub, err := os.ReadFile(sshKey + ".pub")
	mustDo(t, err)

	type mode struct {
		name   string
		create []string // backup flags
		env    []string // for both backup and restore
		ident  string   // restore --identity
	}
	modes := []mode{
		{name: "none", create: []string{"--no-encrypt"}},
		{name: "gpg", create: []string{"--passphrase"}, env: []string{"CCENV_BACKUP_PASSPHRASE=matrix passphrase 0123"}},
		{name: "age", create: []string{"-r", agePub}, ident: ageKey},
		{name: "age-ssh", create: []string{"-r", strings.TrimSpace(string(sshPub))}, ident: sshKey},
	}
	tools := []string{"legacy", "berth"}

	var want string
	for _, md := range modes {
		for _, creator := range tools {
			file := filepath.Join(m.home, fmt.Sprintf("%s-%s.tar.zst", creator, md.name))
			m.tool(t, creator, m.src, md.env, append([]string{"backup", bkOrg, "-o", file}, md.create...), nil)
			for _, restorer := range tools {
				for _, from := range []string{"file", "stdin"} {
					name := fmt.Sprintf("%s/%s->%s/%s", md.name, creator, restorer, from)
					t.Run(name, func(t *testing.T) {
						dst := m.newState(t)
						args := []string{"restore", file, "--no-start", "--as", "t-restored"}
						var stdin *os.File
						if from == "stdin" {
							args[1] = "-"
							f, err := os.Open(file)
							mustDo(t, err)
							defer f.Close()
							stdin = f
						}
						if md.ident != "" {
							args = append(args, "--identity", md.ident)
						}
						m.tool(t, restorer, dst, md.env, args, stdin)
						got := listing(t, filepath.Join(dst, "orgs", "t-restored"))
						if want == "" {
							want = got
							checkTree(t, got)
							return
						}
						if got != want {
							t.Errorf("restored tree differs from the first restore:\n--- first\n%s\n--- this one\n%s", want, got)
						}
					})
				}
			}
		}
	}
}

// newState is an empty state root with an orgs/ dir, cleaned up (as root: sshd/ is root's).
func (m *matrixEnv) newState(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "berth-matrix-")
	mustDo(t, err)
	mustDo(t, os.MkdirAll(filepath.Join(d, "orgs"), 0o700))
	t.Cleanup(func() {
		_ = exec.Command("docker", "run", "--rm", "-v", d+":/d", "--entrypoint", "rm", "claude-env", "-rf", "/d/orgs", "/d/berth", "/d/backups").Run()
		_ = os.RemoveAll(d)
	})
	return d
}

// fixture is a synthetic t-bk with secrets-like files, a regenerable dir and a root-owned sshd key.
func (m *matrixEnv) fixture(t *testing.T) {
	t.Helper()
	o := filepath.Join(m.src, "orgs", bkOrg)
	files := map[string]string{
		"org.env":                        "GIT_USER_NAME=Test User\nBIND_ADDR=127.0.0.1\nSSH_PORT=2291\nTTYD_PORT=7791\nREMOTE_CONTROL=0\n",
		"config/repos.txt":               "# repos\napp git@github.com:acme/app.git\n",
		"config/secrets/ttyd_credential": "node:MATRIXpassword",
		// A migrated org's secrets (#37) must come back byte for byte, 0600.
		"config/secrets/env/CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-MATRIX",
		"ssh/id_ed25519":               "FAKE PRIVATE KEY\n",
		"claude/.credentials.json":     "{}",
		"workspace/app/README.md":      "# app\n",
		"workspace/app/node_modules/x": "regenerable\n",
		"mise/config/config.toml":      "[tools]\ngo = \"latest\"\n",
	}
	for p, c := range files {
		full := filepath.Join(o, p)
		mustDo(t, os.MkdirAll(filepath.Dir(full), 0o700))
		mustDo(t, os.WriteFile(full, []byte(c), 0o600))
	}
	mustDo(t, os.Chmod(filepath.Join(o, "workspace", "app", "README.md"), 0o644))
	docker(t, "run", "--rm", "-v", o+":/o", "--entrypoint", "sh", "claude-env", "-c",
		"mkdir -p /o/sshd && printf 'HOST KEY\\n' > /o/sshd/ssh_host_ed25519_key && chmod 600 /o/sshd/ssh_host_ed25519_key && chown -R 0:0 /o/sshd")
}

// tool runs legacy ccenv or berth against the state root.
func (m *matrixEnv) tool(t *testing.T, which, state string, env, args []string, stdin *os.File) {
	t.Helper()
	var cmd *exec.Cmd
	if which == "legacy" {
		cmd = exec.Command(m.legacy, args...)
		env = append(env, "CCENV_ORGS="+filepath.Join(state, "orgs"), "CCENV_BACKUP_DIR="+filepath.Join(state, "backups"))
	} else {
		cmd = exec.Command(m.berth, append([]string{"--home", state}, args...)...)
	}
	cmd.Env = append(append([]string{}, m.env...), env...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", which, strings.Join(args, " "), err, out)
	}
}

// listing is every entry under dir, as root: path, type, owner (numeric), mode and a content hash.
// org.env loses berth's MANAGER line (a berth restore marks the org as its own), and the manifest
// (which records when and where the backup was made) is left out.
func listing(t *testing.T, dir string) string {
	t.Helper()
	script := `cd /o && sed -i '1{/^MANAGER=berth$/d}' org.env && find . -mindepth 1 ! -name .ccenv-manifest.json -printf '%P %y %U:%G %m\n' | sort &&
find . -type f ! -name .ccenv-manifest.json -exec sha256sum {} + | sort -k2`
	return docker(t, "run", "--rm", "-v", dir+":/o", "--entrypoint", "sh", "claude-env", "-c", script)
}

// checkTree: the first restore has what matters (the rest are compared with it).
func checkTree(t *testing.T, got string) {
	t.Helper()
	uid, gid := os.Getuid(), os.Getgid()
	for _, want := range []string{
		"sshd/ssh_host_ed25519_key f 0:0 600",
		fmt.Sprintf("config/secrets/ttyd_credential f %d:%d 600", uid, gid),
		fmt.Sprintf("config/secrets/env/CLAUDE_CODE_OAUTH_TOKEN f %d:%d 600", uid, gid),
		fmt.Sprintf("workspace/app/README.md f %d:%d 644", uid, gid),
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("restored tree lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "node_modules") {
		t.Errorf("node_modules was backed up:\n%s", got)
	}
	if strings.Contains(got, ".ccenv-format") {
		t.Errorf(".ccenv-format was left in the org:\n%s", got)
	}
}
