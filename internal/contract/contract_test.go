package contract

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/ar4mirez/berth"
)

func shipped(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(berth.Assets, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestImageAndComposeAgree checks every contract value against image/ and compose.yml as berth
// ships them (the embedded copy, which is also what legacy runs through legacy/image).
func TestImageAndComposeAgree(t *testing.T) {
	compose := shipped(t, "compose.yml")
	entrypoint := shipped(t, "image/entrypoint.sh")
	dockerfile := shipped(t, "image/Dockerfile")
	firewall := shipped(t, "image/init-firewall.sh")
	policy := shipped(t, "image/repo-policy.sh")
	guard := shipped(t, "image/repo-guard.js")
	secretsEnv := shipped(t, "image/secrets-env.sh")

	checks := []struct {
		what, in, want string
	}{
		{"container name", compose, "container_name: " + Container("${ORG}")},
		{"org.env is the container's env", compose, "env_file: ${ORG_DIR}/org.env"},
		{"workspace mount", compose, "${ORG_DIR}/workspace:" + Workspace + "\n"},
		{"claude dir mount", compose, "${ORG_DIR}/claude:" + ClaudeDir + "\n"},
		{"config mount, read-only", compose, "${ORG_DIR}/config:" + Config + ":ro"},
		{"ssh port", compose, "${SSH_PORT}:" + SSHPortInside + `"`},
		{"ttyd port", compose, "${TTYD_PORT}:" + TTYDPortInside + `"`},
		{"CLAUDE_CONFIG_DIR", dockerfile, "CLAUDE_CONFIG_DIR=" + ClaudeDir},
		{"repos.txt (repo-policy.sh)", policy, `REPOS_FILE="${REPOS_FILE:-` + ReposFile + `}"`},
		{"repos.txt (repo-guard.js)", guard, "const REPOS = '" + ReposFile + "';"},
		{"firewall.txt", firewall, "CONF=" + FirewallFile + "\n"},
		{"firewall status", firewall, "STATUS=" + FirewallStatus + "\n"},
		{"firewall presets subcommand, apply the default", firewall, `if [ "${1:-apply}" = "presets" ]; then`},
		{"authorized_keys", entrypoint, "install -o root -g root -m 644 " + AuthorizedKeys + " "},
		{"ttyd credential", entrypoint, `-c "$(cat ` + TTYDCredential + `)"`},
		{"tmux session (created)", entrypoint, "tmux new-session -d -s " + TmuxSession + " "},
		{"tmux session (ttyd joins it)", entrypoint, "tmux new-session -A -s " + TmuxSession + " "},
		{"Remote Control log", entrypoint, `RC_LOG="$CFG/` + strings.TrimPrefix(RemoteControlLog, ClaudeDir+"/") + `"`},
		{"Remote Control log dir", entrypoint, "CFG=" + ClaudeDir + "\n"},
		{"custom env vars snapshot for SSH", entrypoint, "${" + EnvKeys + ":-}"},
		{"secrets as files: the dir", secretsEnv, "for _f in " + SecretsEnv + "/*; do"},
		{"secrets as files: loaded before the snapshot", entrypoint, ". /usr/local/lib/claude-env/secrets-env.sh\n"},
		{"secrets as files: shipped", dockerfile, "secrets-env.sh uid-remap.sh /usr/local/lib/claude-env/"},
		{"runtime UID remap: compose passes the host user", compose, "HOST_UID: ${HOST_UID:-}\n      HOST_GID: ${HOST_GID:-}\n"},
		{"runtime UID remap: first in the entrypoint", entrypoint, ". /usr/local/lib/claude-env/uid-remap.sh\n"},
	}
	for _, c := range checks {
		if !strings.Contains(c.in, c.want) {
			t.Errorf("%s: the image no longer has %q", c.what, c.want)
		}
	}
	// The init-firewall.sh berth runs must be the one on the image's PATH.
	if !regexp.MustCompile(`(?m)^COPY .*\b` + regexp.QuoteMeta(FirewallScript) + `\b.* /usr/local/bin/`).MatchString(dockerfile) {
		t.Errorf("Dockerfile doesn't put %s in /usr/local/bin", FirewallScript)
	}
}
