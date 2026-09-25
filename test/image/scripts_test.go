// Package image checks the container image's scripts without building the image: every shell
// script parses (bash -n) and every JS file compiles (node --check). The files are discovered, so
// a new script is covered without touching this test.
package image

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const dir = "../../image"

func kind(t *testing.T, path string) string {
	t.Helper()
	switch filepath.Ext(path) {
	case ".sh":
		return "sh"
	case ".js":
		return "js"
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	first, _ := bufio.NewReader(f).ReadString('\n')
	switch {
	case !strings.HasPrefix(first, "#!"):
		return ""
	case strings.Contains(first, "bash") || strings.HasSuffix(strings.TrimSpace(first), "/sh"):
		return "sh"
	case strings.Contains(first, "node"):
		return "js"
	}
	return ""
}

func TestScriptsParse(t *testing.T) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(dir, e.Name())
		var cmd *exec.Cmd
		switch kind(t, p) {
		case "sh":
			cmd = exec.Command("bash", "-n", p)
		case "js":
			cmd = exec.Command("node", "--check", p)
		default:
			continue
		}
		if _, err := exec.LookPath(cmd.Args[0]); err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("%s is required in CI", cmd.Args[0])
			}
			t.Logf("no %s: skipping %s", cmd.Args[0], e.Name())
			continue
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s", e.Name(), err, out)
		}
		checked++
	}
	if checked < 5 {
		t.Errorf("only %d scripts checked; expected the image's shell and JS files", checked)
	}
}
