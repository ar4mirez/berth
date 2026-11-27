package image

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeMise is mise as berth-tools runs it: it records its arguments and the checks it was given,
// fails a tool whose name has "bad" in it, as a failed attestation would, and resolves a tool whose
// name has "fuzzy" in it to another version, as a prefix match would.
const fakeMise = `#!/usr/bin/env bash
echo "$* paranoid=$MISE_PARANOID attest=$MISE_GITHUB_ATTESTATIONS slsa=$MISE_SLSA cosign=$MISE_AQUA_COSIGN" >> "$FAKE_LOG"
case "$1 $2" in
  "install "*bad*) echo "mise ERROR GitHub artifact attestation verification failed"; echo "for $*"; exit 1 ;;
  "install "*) exit 0 ;;
  "where "*fuzzy*) echo "/data/installs/fuzzy/1.4.3" ;;
  "where "*) echo "/data/installs/x/${2##*@}" ;;
esac
`

// berthTools runs image/berth-tools on a list (none when list is nil), with the fake mise and
// MISE_CONFIG_DIR in d, and returns the status ("" when it wrote none) and berth's mise config.
func berthTools(t *testing.T, d string, list *string) (status, conf string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(dir, "berth-tools"))
	if err != nil {
		t.Fatal(err)
	}
	mise, log, lst, st := filepath.Join(d, "mise"), filepath.Join(d, "mise.log"), filepath.Join(d, "tools.txt"), filepath.Join(d, "tools.status")
	if err := os.WriteFile(mise, []byte(fakeMise), 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(lst)
	if list != nil {
		if err := os.WriteFile(lst, []byte(*list), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := exec.Command(script)
	c.Env = append(os.Environ(), "BERTH_TOOLS_LIST="+lst, "BERTH_TOOLS_STATUS="+st, "BERTH_TOOLS_MISE="+mise, "FAKE_LOG="+log,
		"MISE_CONFIG_DIR="+filepath.Join(d, "config"))
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("berth-tools: %v\n%s", err, out)
	}
	s, _ := os.ReadFile(st)
	cf, _ := os.ReadFile(filepath.Join(d, "config", "conf.d", "berth-tools.toml"))
	return string(s), string(cf)
}

// TestBerthTools: image/berth-tools, which installs an org's pinned tools at its start (#216). Each
// is installed with mise's checks on and must resolve to its exact version; one that fails is
// reported with the reason, never skipped; a line that isn't a pin (a prefix version too) is
// reported, not run; the status ends with "done". Only the tools that installed are in the mise
// config berth owns.
func TestBerthTools(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getuid() == 0 {
		t.Skip("berth-tools runs in the Linux image, as the container's user")
	}
	d := t.TempDir()
	list := "# a header\ngadget github:acme/gadget 1.4.2\nbad aqua:acme/bad 2.0.0\nevil github:acme/evil 1.0.0;id\n" +
		"short github:acme/short 1.4\nfuzzy github:acme/fuzzy 1.4.2\nwidget github:acme/widget v0.1.0"
	got, conf := berthTools(t, d, &list)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 7 || lines[0] != "gadget github:acme/gadget@1.4.2 ok" ||
		!strings.HasPrefix(lines[1], "bad aqua:acme/bad@2.0.0 failed mise ERROR GitHub artifact attestation verification failed for install aqua:acme/bad@2.0.0") ||
		!strings.HasPrefix(lines[2], "evil - failed not a valid pin") || !strings.HasPrefix(lines[3], "short - failed not a valid pin") ||
		lines[4] != "fuzzy github:acme/fuzzy@1.4.2 failed mise installed 1.4.3, not 1.4.2" ||
		lines[5] != "widget github:acme/widget@v0.1.0 ok" || lines[6] != "done" {
		t.Errorf("status:\n%s", got)
	}
	ran, _ := os.ReadFile(filepath.Join(d, "mise.log"))
	if strings.Contains(string(ran), "evil") || strings.Contains(string(ran), "short") || strings.Contains(string(ran), "use ") ||
		strings.Count(string(ran), "install ") != 4 || strings.Count(string(ran), "paranoid=1 attest=1 slsa=1 cosign=1") != 4 ||
		!strings.Contains(string(ran), "install github:acme/gadget@1.4.2") {
		t.Errorf("mise ran:\n%s", ran)
	}
	tools := confTools(conf)
	if len(tools) != 2 || tools["github:acme/gadget"] != "1.4.2" || tools["github:acme/widget"] != "v0.1.0" {
		t.Errorf("berth's mise config:\n%s", conf)
	}
}

// TestBerthToolsReplacesConfig: berth's mise config is the list at each start. A pin removed is gone
// from it, a source changed replaces the old one, and with no list it is removed, with no status.
func TestBerthToolsReplacesConfig(t *testing.T) {
	if runtime.GOOS != "linux" || os.Getuid() == 0 {
		t.Skip("berth-tools runs in the Linux image, as the container's user")
	}
	d := t.TempDir()
	first := "gadget github:acme/gadget 1.4.2\nwidget github:acme/widget 2.0.0\n"
	if _, conf := berthTools(t, d, &first); len(confTools(conf)) != 2 {
		t.Fatalf("first start:\n%s", conf)
	}
	second := "gadget aqua:acme/gadget 1.4.2\n"
	status, conf := berthTools(t, d, &second)
	if tools := confTools(conf); len(tools) != 1 || tools["aqua:acme/gadget"] != "1.4.2" {
		t.Errorf("a pin removed and a source changed:\n%s\nstatus:\n%s", conf, status)
	}
	status, conf = berthTools(t, d, nil)
	if _, err := os.Stat(filepath.Join(d, "config", "conf.d", "berth-tools.toml")); !os.IsNotExist(err) || status != "" || conf != "" {
		t.Errorf("no list: config %q (%v), status %q", conf, err, status)
	}
}

// confTools is the [tools] table of berth's mise config, read as mise would: "source" = "version".
func confTools(conf string) map[string]string {
	out := map[string]string{}
	in := false
	for _, l := range strings.Split(conf, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || strings.HasPrefix(l, "#"):
		case strings.HasPrefix(l, "["):
			in = l == "[tools]"
		case in:
			k, v, ok := strings.Cut(l, " = ")
			if ok {
				out[strings.Trim(k, `"`)] = strings.Trim(v, `"`)
			}
		}
	}
	return out
}
