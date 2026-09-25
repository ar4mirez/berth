package parity

// Phase 4a: backup (every option) and keygen.

import (
	"slices"
	"strings"
	"testing"
)

const planOut = "kept: 12 files, 3.1MiB\nskipped: node_modules (2 dirs, 40MiB)\nskipped: .cache (1 dir, 5MiB)\nextra line\n"

// archiveRules answer archive.sh's plan and create in the backup engine.
func archiveRules(more ...Rule) []Rule {
	return append(more,
		Rule{Bin: "docker", Match: `/archive\.sh plan$`, Stdout: planOut},
		Rule{Bin: "docker", Match: `/archive\.sh create$`, Stdout: "ARCHIVE-BYTES"})
}

const bk = "state/backups/"

// withBackups: acme's older backups, and names prune must leave alone.
var withBackups = merge(withKey, map[string]File{
	"state/backups":                          {Dir: true},
	bk + "acme-20250101-000000.tar.zst":      {Content: "old1"},
	bk + "acme-20250102-000000.tar.zst.gpg":  {Content: "old2"},
	bk + "acme-20251231-235959.tar.zst.age":  {Content: "old3"},
	bk + "acme-test-20250101-000000.tar.zst": {Content: "other org"},
	bk + "acme-2025.tar.zst":                 {Content: "not a stamp"},
	bk + "acme-20240101-000000.tar.zst.bak":  {Content: "wrong suffix"},
	bk + "acme-20230101-000000.tar.zst":      {Dir: true},
	bk + "globex-20250101-000000.tar.zst":    {Content: "globex"},
	"home/recipients.txt":                    {Content: "# team keys\nage1teamkey\nssh-ed25519 AAAAFAKE ops@example\n  age1indented\n"},
	"home/nokeys.txt":                        {Content: "# nothing here\n"},
	"home/bk":                                {Dir: true},
})

const pass = "correct horse battery"

func init() {
	pp := []string{"CCENV_BACKUP_PASSPHRASE=" + pass}
	more := []checked{
		{Scenario{Name: "backup, key file", Args: []string{"backup", "acme"}, Files: withKey, Rules: archiveRules()},
			0, []string{"== acme", "skipped: .cache", "Wrote <RUN>/state/backups/acme-20260102-030405.tar.zst.age (13B, key-encrypted)"}},
		{Scenario{Name: "backup --no-encrypt --keep 2", Args: []string{"backup", "--no-encrypt", "acme", "--keep", "2"}, Files: withBackups, Rules: archiveRules()},
			0, []string{"WARNING: --no-encrypt", "NOT encrypted", "Pruned acme-20250102-000000.tar.zst.gpg (keeping newest 2)", "Pruned acme-20250101-000000.tar.zst (keeping newest 2)"}},
		{Scenario{Name: "backup --keep, nothing to prune", Args: []string{"backup", "globex", "--keep", "5"}, Files: withBackups, Rules: archiveRules()}, 0, []string{"Wrote"}},
		{Scenario{Name: "backup, passphrase from the environment", Args: []string{"backup", "globex"}, Env: pp, Files: twoOrgs, Rules: archiveRules()},
			0, []string{"(13B, passphrase-encrypted)"}},
		{Scenario{Name: "backup --passphrase over the key", Args: []string{"backup", "--passphrase", "globex"}, Env: []string{"CCENV_BACKUP_PASSPHRASE=short"},
			Files: withKey, Rules: archiveRules()}, 0, []string{"warning: passphrase shorter than 12 characters", ".tar.zst.gpg"}},
		{Scenario{Name: "backup, recipients from the environment", Args: []string{"backup", "acme"}, Env: []string{"CCENV_BACKUP_RECIPIENTS=age1aaa,age1bbb age1ccc", "CCENV_BACKUP_PASSPHRASE=ignored"},
			Files: withKey, Rules: archiveRules()}, 0, []string{"key-encrypted"}},
		// ${CCENV_BACKUP_RECIPIENTS//,/ } is word-split: an ssh key's spaces split it too.
		{Scenario{Name: "backup, ssh key in the recipients variable", Args: []string{"backup", "acme"}, Env: []string{"CCENV_BACKUP_RECIPIENTS=ssh-ed25519 AAAAFAKE"},
			Files: twoOrgs, Rules: archiveRules()}, 1, []string{"<tool>: not a recipient: AAAAFAKE"}},
		{Scenario{Name: "backup -r, keys and a file", Args: []string{"backup", "acme", "-r", "age1direct", "--recipient", "recipients.txt", "-r", "ssh-rsa AAAAFAKE"},
			Files: withBackups, Rules: archiveRules()}, 0, []string{"key-encrypted"}},
		{Scenario{Name: "backup -r, file without keys", Args: []string{"backup", "acme", "-r", "nokeys.txt"}, Files: withBackups, Rules: archiveRules()},
			1, []string{"<tool>: no age/ssh public key in nokeys.txt"}},
		{Scenario{Name: "backup -r, not a recipient", Args: []string{"backup", "acme", "-r", "nope.txt"}, Files: withBackups}, 1, []string{"<tool>: not a recipient: nope.txt"}},
		// A key file with no age1 key: recips+=("$(grep …)") fails under set -e.
		{Scenario{Name: "backup, key file without a key", Args: []string{"backup", "acme"}, Files: withFile(twoOrgs, "home/.config/ccenv/backup.key", File{Content: "junk\n"})}, 1, nil},
		{Scenario{Name: "backup --plan", Args: []string{"backup", "--plan", "acme", "globex"}, Files: twoOrgs, Rules: archiveRules()},
			0, []string{"== acme", "extra line", "== globex"}},
		{Scenario{Name: "backup --dry-run, no encryption set up", Args: []string{"backup", "globex", "--dry-run", "--passphrase"}, Files: twoOrgs, Rules: archiveRules()}, 0, []string{"== globex"}},
		{Scenario{Name: "backup -o -", Args: []string{"backup", "acme", "-o", "-", "--no-encrypt"}, Files: twoOrgs, Rules: archiveRules()}, 0, []string{"ARCHIVE-BYTES", "WARNING"}},
		{Scenario{Name: "backup -o dir, two orgs", Args: []string{"backup", "--output", "bk", "acme", "globex", "--keep", "1"}, Files: withBackups, Rules: archiveRules()},
			0, []string{"Wrote bk/acme-20260102-030405.tar.zst.age", "Wrote bk/globex-"}},
		{Scenario{Name: "backup -o file", Args: []string{"backup", "acme", "-o", "one.tar", "--keep", "1"}, Files: withBackups, Rules: archiveRules()}, 0, []string{"Wrote one.tar (13B"}},
		{Scenario{Name: "backup -o file, two orgs", Args: []string{"backup", "acme", "globex", "-o", "one.tar"}, Files: withKey},
			1, []string{"<tool>: -o must be a directory when backing up several orgs"}},
		{Scenario{Name: "backup -o -, two orgs", Args: []string{"backup", "acme", "globex", "-o", "-"}, Files: withKey}, 1, []string{"-o must be a directory"}},
		{Scenario{Name: "backup --all", Args: []string{"backup", "--all"}, Files: withKey, Rules: archiveRules()}, 0, []string{"== acme", "== globex"}},
		{Scenario{Name: "backup --all, no orgs", Args: []string{"backup", "--all"}}, 1, []string{"<tool>: usage: <tool> backup <org>...|--all"}},
		{Scenario{Name: "backup, no orgs", Args: []string{"backup", "--plan"}, Files: twoOrgs}, 1, []string{"usage: <tool> backup"}},
		{Scenario{Name: "backup --keep 0", Args: []string{"backup", "acme", "--keep", "0"}, Files: twoOrgs}, 1, []string{"<tool>: --keep needs a number >= 1"}},
		{Scenario{Name: "backup --keep without a number", Args: []string{"backup", "acme", "--keep"}, Files: twoOrgs}, 1, []string{"--keep needs a number"}},
		{Scenario{Name: "backup, unknown flag", Args: []string{"backup", "acme", "--frob"}, Files: twoOrgs}, 1, []string{"<tool>: unknown flag --frob"}},
		// need_org runs per org, after encryption is set up: the orgs before it are backed up.
		{Scenario{Name: "backup, unknown org", Args: []string{"backup", "acme", "nope"}, Files: withKey, Rules: archiveRules()},
			1, []string{"Wrote", "<tool>: unknown org 'nope' (run: <tool> init nope)"}},
		{Scenario{Name: "backup, engine fails", Args: []string{"backup", "acme"}, Files: withKey,
			Rules: archiveRules(Rule{Bin: "docker", Match: `/archive\.sh create$`, Stdout: "partial", Stderr: "zstd: error\n", Exit: 1})},
			1, []string{"zstd: error", "<tool>: backup of acme failed"}},
		{Scenario{Name: "backup, plan fails", Args: []string{"backup", "acme"}, Files: withKey,
			Rules: archiveRules(Rule{Bin: "docker", Match: `/archive\.sh plan$`, Stdout: "one\n", Stderr: "jq: error\n", Exit: 3})}, 3, []string{"jq: error"}},
		{Scenario{Name: "backup -o -, engine fails", Args: []string{"backup", "acme", "-o", "-"}, Files: withKey,
			Rules: archiveRules(Rule{Bin: "docker", Match: `/archive\.sh create$`, Exit: 4})}, 4, nil},
		{Scenario{Name: "backup, image missing", Args: []string{"backup", "--plan", "acme"}, Files: twoOrgs,
			Rules: archiveRules(Rule{Bin: "docker", Match: `^image inspect `, Exit: 1})}, 0, []string{"== acme"}},
		{Scenario{Name: "backup, image without age", Args: []string{"backup", "--plan", "acme"}, Files: twoOrgs,
			Rules: archiveRules(Rule{Bin: "docker", Match: `command -v age`, Exit: 1}, Rule{Bin: "docker", Match: `^build `, Exit: 6})}, 6, nil},

		// keygen
		{Scenario{Name: "keygen", Args: []string{"keygen"}, Env: []string{"CCENV_BACKUP_KEY=keys/backup.key"}, Files: twoOrgs,
			Rules: []Rule{{Bin: "docker", Match: `--entrypoint age-keygen`, Stdout: backupKey, Stderr: "Public key: age1parityfake\n"}}},
			0, []string{"Created keys/backup.key", "Public key: age1parityfake", "before '<tool> restore'"}},
		{Scenario{Name: "keygen, key exists", Args: []string{"keygen"}, Files: withKey},
			1, []string{"<tool>: <RUN>/home/.config/ccenv/backup.key already exists (public key: age1parityfake)"}},
		{Scenario{Name: "keygen, age-keygen fails", Args: []string{"keygen"}, Env: []string{"CCENV_BACKUP_KEY=keys/backup.key"}, Files: twoOrgs,
			Rules: []Rule{{Bin: "docker", Match: `--entrypoint age-keygen`, Stderr: "hidden\n", Exit: 2}}}, 2, nil},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
}

// TestBerthKeyFile: berth's key is ~/.config/berth/backup.key. ccenv's is used while berth has none
// (so the live key keeps working after cutover), and keygen won't shadow it with a second key
// (PARITY.md).
func TestBerthKeyFile(t *testing.T) {
	keygen := []Rule{{Bin: "docker", Match: `--entrypoint age-keygen`, Stdout: backupKey}}
	r := run(t, BerthUnowned(berthBin), Scenario{Args: []string{"keygen"}, Files: twoOrgs, Rules: keygen})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "Created <RUN>/home/.config/berth/backup.key\n") ||
		!slices.Contains(r.Tree, "home/.config/berth/ 0700") {
		t.Errorf("keygen: exit %d, stdout %q\n%s", r.Exit, r.Stdout, strings.Join(r.Tree, "\n"))
	}
	own := withFile(withKey, "home/.config/berth/backup.key", File{Content: "AGE-SECRET-KEY-1OWN\n# public key: age1berthown\n"})
	r = run(t, BerthUnowned(berthBin), Scenario{Args: []string{"backup", "acme"}, Files: own, Rules: archiveRules()})
	if r.Exit != 0 || !slices.ContainsFunc(r.Calls, func(c string) bool { return strings.Contains(c, `{secret recipients: "0644 age1berthown\n"}`) }) {
		t.Errorf("backup with berth's own key: exit %d, stderr %q, calls:\n%s", r.Exit, r.Stderr, strings.Join(r.Calls, "\n"))
	}
	r = run(t, BerthUnowned(berthBin), Scenario{Args: []string{"keygen"}, Env: []string{"BERTH_BACKUP_KEY=k/b.key", "CCENV_BACKUP_KEY=ignored"}, Files: withKey, Rules: keygen})
	if r.Exit != 0 || !strings.Contains(r.Stdout, "Created k/b.key\n") {
		t.Errorf("keygen with BERTH_BACKUP_KEY: exit %d, stdout %q, stderr %q", r.Exit, r.Stdout, r.Stderr)
	}
}

// TestBerthBackupAllOwnsOnly: backup --all is berth's orgs only (ccenv's --all skips berth's), while
// an org named explicitly can be either tool's.
func TestBerthBackupAllOwnsOnly(t *testing.T) {
	files := withFile(withKey, "state/orgs/acme/org.env", File{Content: "MANAGER=berth\n" + acmeEnv})
	r := run(t, BerthUnowned(berthBin), Scenario{Args: []string{"backup", "--all"}, Files: files, Rules: archiveRules()})
	if r.Exit != 0 || !strings.Contains(r.Stderr, "== acme\n") || strings.Contains(r.Stderr, "globex") {
		t.Errorf("--all: exit %d, stderr %q; want acme only", r.Exit, r.Stderr)
	}
	r = run(t, BerthUnowned(berthBin), Scenario{Args: []string{"backup", "globex"}, Files: files, Rules: archiveRules()})
	if r.Exit != 0 || !strings.Contains(r.Stderr, "Wrote <RUN>/state/backups/globex-") {
		t.Errorf("legacy-owned globex by name: exit %d, stderr %q", r.Exit, r.Stderr)
	}
}
