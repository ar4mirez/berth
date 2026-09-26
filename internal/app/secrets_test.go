package app

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host/local"
)

// TestExecLoader runs the inline loader berth puts in front of `claude`/`run` for a migrated org
// (#37): it exports the files' values, skips invalid names, lets a file win over the container's
// environment, and runs the command with its arguments intact. An org that isn't migrated gets
// its command unchanged.
func TestExecLoader(t *testing.T) {
	root := t.TempDir()
	st := config.State{Home: config.Home{Path: root}}
	a := New(st, local.New(), nil, io.Discard, io.Discard, os.Getenv)
	if got := a.execLoader("acme", "claude", "-p", "hi"); strings.Join(got, " ") != "claude -p hi" {
		t.Errorf("not migrated: %q", got)
	}

	secrets := filepath.Join(root, "orgs", "acme", "config", "secrets", "env")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-FILE", "OPENROUTER_API_KEY": "a b 'c'", "bad-name": "x"} {
		if err := os.WriteFile(filepath.Join(secrets, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	argv := a.execLoader("acme", "sh", "-c", `printf '%s|%s|%s' "$CLAUDE_CODE_OAUTH_TOKEN" "$OPENROUTER_API_KEY" "$1"`, "x", "arg with space")
	if argv[0] != "sh" || argv[1] != "-c" || argv[3] != "berth-secrets" {
		t.Fatalf("argv: %q", argv)
	}
	// Run it here, with the container path pointed at the test's directory.
	argv[2] = strings.ReplaceAll(argv[2], contract.SecretsEnv, secrets)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_OAUTH_TOKEN=from-container-env")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if got, want := out.String(), "sk-FILE|a b 'c'|arg with space"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
