package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretsEnv runs image/secrets-env.sh (#37) against a directory like the org's
// /config/secrets/env: valid variable names are exported, others skipped, and a file wins over a
// value already in the environment (an org.env value, before the next restart).
func TestSecretsEnv(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(dir, "secrets-env.sh"))
	if err != nil {
		t.Fatal(err)
	}
	d := t.TempDir()
	for name, v := range map[string]string{
		"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-FILE",
		"OPENROUTER_API_KEY":      "with spaces and 'quotes'",
		"bad-name":                "x",
		"1LEADING_DIGIT":          "x",
	} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(t.TempDir(), "secrets-env.sh")
	if err := os.WriteFile(script, []byte(strings.ReplaceAll(string(src), "/config/secrets/env", d)), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `set -euo pipefail; . "$1"; printf '%s|%s|%s|%s' "$CLAUDE_CODE_OAUTH_TOKEN" "$OPENROUTER_API_KEY" "$(env | grep -c '^bad-name=' || true)" "${_f-unset}"`, "x", script)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN=from-org-env")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got, want := string(out), "sk-ant-oat01-FILE|with spaces and 'quotes'|0|unset"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// No directory (an org not migrated): nothing changes, and set -e/-u don't trip.
	empty := filepath.Join(t.TempDir(), "secrets-env.sh")
	if err := os.WriteFile(empty, []byte(strings.ReplaceAll(string(src), "/config/secrets/env", filepath.Join(d, "missing"))), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("bash", "-c", `set -euo pipefail; . "$1"; printf '%s' "$CLAUDE_CODE_OAUTH_TOKEN"`, "x", empty)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN=from-org-env")
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "from-org-env" {
		t.Errorf("not migrated: %q, %v", out, err)
	}
}
