package parity

import (
	"slices"
	"strings"
	"testing"
)

// Phase 4c: restore, rehydrate, migrate. The fake engine "extracts" by writing a rule's Dst files
// into the staging dir mounted at /dst.

const restoredEnv = "CLAUDE_CODE_OAUTH_TOKEN=\nGIT_USER_NAME=Test User\nBIND_ADDR=tailscale\nSSH_PORT=2201\nTTYD_PORT=7790\nREMOTE_CONTROL=0\n"

// extracted is what a backup of org holds (manifest, format marker, org.env and a little more).
func extracted(org, env string) map[string]string {
	return map[string]string{
		".ccenv-manifest.json": `{"org":"` + org + `","source_host":"old-host","created":"2026-01-01T00:00:00Z","files":12}`,
		".ccenv-format":        "ccenv-backup-v1\n",
		"org.env":              env,
		"config/repos.txt":     "# repos\nnotes local\n",
		"workspace/notes/":     "",
		"ssh/id_ed25519":       "FAKE PRIVATE KEY\n",
	}
}

// extractRule answers archive.sh extract with files.
func extractRule(files map[string]string) Rule {
	return Rule{Bin: "docker", Match: `/archive\.sh extract [0-9]+ [0-9]+$`, Stdout: "extracted\n", Dst: files}
}

var backupFiles = merge(twoOrgs, map[string]File{
	"home/b.tar.zst":     {Content: "\x28\xb5\x2f\xfdzstd"},
	"home/b.tar.zst.age": {Content: "age-encryption.org/v1\n"},
	"home/b.tar.zst.gpg": {Content: "\x8c\x0dgpg"},
	"home/b.bin":         {Content: "\x00odd"},
	"home/empty":         {Content: ""},
	"home/id.txt":        {Content: "AGE-SECRET-KEY-1IDENTITY\n"},
})

func init() {
	fresh := extracted("t-restored", restoredEnv)
	pp := []string{"CCENV_BACKUP_PASSPHRASE=" + pass}
	more := []checked{
		// Ports taken here are reassigned (SSH_PORT 2201 is acme's), and with Tailscale down the org
		// is bound to 127.0.0.1.
		{Scenario{Name: "restore --no-start, zstd", Args: []string{"restore", "b.tar.zst", "--no-start"}, Files: backupFiles,
			Rules: []Rule{extractRule(fresh), {Bin: "tailscale", Match: `^ip -4$`, Exit: 1}}},
			0, []string{"Restoring 't-restored' from old-host (2026-01-01T00:00:00Z, ccenv-backup-v1) as 't-restored'", "SSH_PORT was taken here; now 2203",
				"Tailscale not up on this host", "Restored to <RUN>/state/orgs/t-restored", "Start with: <tool> up t-restored"}},
		{Scenario{Name: "restore, age with the key file, started", Args: []string{"restore", "b.tar.zst.age", "--no-rehydrate", "--as", "t-copy"},
			Files: merge(backupFiles, withKey), Rules: []Rule{extractRule(fresh), {Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"}}},
			0, []string{"as 't-copy'", "Restored to <RUN>/state/orgs/t-copy", "t-copy"}},
		{Scenario{Name: "restore, age without a key", Args: []string{"restore", "b.tar.zst.age"}, Files: backupFiles},
			1, []string{"<tool>: key-encrypted backup: pass --identity <key> (default <RUN>/home/.config/ccenv/backup.key not found)"}},
		{Scenario{Name: "restore, gpg with --identity too", Args: []string{"restore", "-i", "id.txt", "b.tar.zst.gpg", "--no-start"}, Env: pp,
			Files: backupFiles, Rules: []Rule{extractRule(fresh)}}, 0, []string{"Restored to"}},
		{Scenario{Name: "restore, unknown format, passphrase set", Args: []string{"restore", "b.bin", "--no-start"}, Env: pp, Files: merge(backupFiles, withKey),
			Rules: []Rule{extractRule(fresh)}}, 0, []string{"Restored to"}},
		{Scenario{Name: "restore, empty file", Args: []string{"restore", "empty", "--no-start"}, Files: backupFiles, Rules: []Rule{extractRule(fresh)}}, 0, []string{"Restored to"}},
		{Scenario{Name: "restore -", Args: []string{"restore", "-", "--no-start", "--as", "t-piped"}, Stdin: "PLAIN-STREAM", Env: pp, Files: withKey,
			Rules: []Rule{{Bin: "docker", Match: `/archive\.sh extract `, Stdin: true, Dst: fresh}}}, 0, []string{"as 't-piped'"}},
		{Scenario{Name: "restore, org exists", Args: []string{"restore", "b.tar.zst"}, Files: backupFiles, Rules: []Rule{extractRule(extracted("acme", acmeEnv))}},
			1, []string{"<tool>: acme already exists (use --force to replace it, or --as <other-name>)"}},
		{Scenario{Name: "restore --force, running", Args: []string{"restore", "--force", "b.tar.zst", "--no-start"}, Files: backupFiles,
			Rules: running("acme", extractRule(extracted("acme", acmeEnv)))},
			0, []string{"Existing acme moved to <RUN>/state/backups/.replaced/", "Start with: <tool> up acme"}},
		{Scenario{Name: "restore, extract fails", Args: []string{"restore", "b.tar.zst"}, Files: backupFiles,
			Rules: []Rule{{Bin: "docker", Match: `/archive\.sh extract `, Stderr: "gpg: decryption failed\n", Exit: 2, Dst: map[string]string{"partial": "x"}}}},
			1, []string{"gpg: decryption failed", "<tool>: restore failed (wrong passphrase/key, or the backup is corrupted/tampered). Nothing was changed."}},
		{Scenario{Name: "restore, no manifest", Args: []string{"restore", "b.tar.zst"}, Files: backupFiles, Rules: []Rule{extractRule(map[string]string{"org.env": restoredEnv})}},
			1, []string{"<tool>: not a <tool> backup (no manifest)"}},
		{Scenario{Name: "restore, invalid name", Args: []string{"restore", "b.tar.zst", "--as", "Bad_Name"}, Files: backupFiles, Rules: []Rule{extractRule(fresh)}},
			1, []string{"<tool>: invalid org name 'Bad_Name'"}},
		{Scenario{Name: "restore, no format marker", Args: []string{"restore", "b.tar.zst", "--no-start"}, Files: backupFiles,
			Rules: []Rule{extractRule(withoutKey(fresh, ".ccenv-format"))}}, 0, []string{"(2026-01-01T00:00:00Z, ) as"}},
		{Scenario{Name: "restore, no arguments", Args: []string{"restore", "--force"}, Files: twoOrgs},
			1, []string{"<tool>: usage: <tool> restore <file|-> [--as name] [--identity key] [--force] [--no-start] [--no-rehydrate]"}},
		{Scenario{Name: "restore, no such file", Args: []string{"restore", "nope.tar.zst"}, Files: twoOrgs}, 1, []string{"<tool>: no such file: nope.tar.zst"}},
		{Scenario{Name: "restore, unknown flag", Args: []string{"restore", "b.tar.zst", "--frob"}, Files: backupFiles}, 1, []string{"<tool>: unknown flag --frob"}},
		{Scenario{Name: "restore, identity not found", Args: []string{"restore", "b.tar.zst", "-i", "missing.key"}, Files: backupFiles},
			1, []string{"<tool>: identity not found: missing.key"}},
		{Scenario{Name: "restore, start and rehydrate", Args: []string{"restore", "b.tar.zst"}, Files: backupFiles, Rules: []Rule{
			extractRule(fresh),
			{Bin: "docker", Match: `^ps --format`, Stdout: "claude-t-restored\n"},
			{Bin: "tailscale", Match: `^ip -4$`, Stdout: "100.64.0.7\n"},
			{Bin: "docker", Match: `bash -s$`, Stdin: true, Stdout: "-- mise toolchains\n-- done\n"}}},
			0, []string{"== Rehydrating t-restored", "All registered repos are present.", "-- done", "t-restored     up"}},
		{Scenario{Name: "restore, start fails", Args: []string{"restore", "b.tar.zst"}, Files: backupFiles,
			Rules: []Rule{extractRule(fresh), {Bin: "docker", Match: `^compose `, Stdout: "hidden\n", Stderr: "port is already allocated\n", Exit: 1}}},
			1, []string{"port is already allocated"}},

		// rehydrate
		{Scenario{Name: "rehydrate", Args: []string{"rehydrate", "acme"}, Files: twoOrgs,
			Rules: running("acme", Rule{Bin: "docker", Match: `bash -s$`, Stdin: true, Stdout: "-- done\n"})},
			0, []string{"== Rehydrating acme: repos, toolchains and dependencies (this can take a while)", "All registered repos are present.", "-- done"}},
		{Scenario{Name: "rehydrate, script fails", Args: []string{"rehydrate", "acme"}, Files: twoOrgs,
			Rules: running("acme", Rule{Bin: "docker", Match: `bash -s$`, Exit: 7})}, 7, nil},
		{Scenario{Name: "rehydrate, not running", Args: []string{"rehydrate", "globex"}, Files: twoOrgs}, 1, []string{"not running"}},

		// migrate
		{Scenario{Name: "migrate", Args: []string{"migrate", "acme", "ops@new-host", "--as", "t-moved"}, Files: twoOrgs, Rules: archiveRules(
			Rule{Bin: "ssh", Match: `command -v`, Stdout: "/usr/local/bin/tool\n"},
			Rule{Bin: "ssh", Match: `restore - `, Stdin: true, Stdout: "Restored to /remote/orgs/t-moved\n"})},
			0, []string{"== Streaming acme to ops@new-host (skipping regenerable data)", "skipped: node_modules", "Restored to /remote/orgs/t-moved",
				"Migrated. 'acme' is still running here too. Once the copy on ops@new-host looks right, stop this one:", "  <tool> down acme     (both"}},
		{Scenario{Name: "migrate, no --as", Args: []string{"migrate", "globex", "new-host"}, Files: twoOrgs, Rules: archiveRules(
			Rule{Bin: "ssh", Match: `command -v`, Stdout: "~/.local/bin/tool\n"}, Rule{Bin: "ssh", Match: `restore - $`, Stdin: true})}, 0, []string{"Migrated."}},
		{Scenario{Name: "migrate, remote restore fails", Args: []string{"migrate", "acme", "new-host"}, Files: twoOrgs, Rules: archiveRules(
			Rule{Bin: "ssh", Match: `command -v`, Stdout: "/usr/local/bin/tool\n"}, Rule{Bin: "ssh", Match: `restore - `, Stdin: true, Stderr: "no space\n", Exit: 3})},
			3, []string{"no space"}},
		{Scenario{Name: "migrate, stream fails", Args: []string{"migrate", "acme", "new-host"}, Files: twoOrgs, Rules: archiveRules(
			Rule{Bin: "ssh", Match: `command -v`, Stdout: "/usr/local/bin/tool\n"}, Rule{Bin: "ssh", Match: `restore - `, Stdin: true},
			Rule{Bin: "docker", Match: `/archive\.sh create$`, Stdout: "part", Exit: 4})}, 4, nil},
		{Scenario{Name: "migrate, no docker there", Args: []string{"migrate", "acme", "new-host"}, Files: twoOrgs,
			Rules: []Rule{{Bin: "ssh", Match: `docker info`, Exit: 255, Stderr: "ssh: connect to host new-host port 22: Connection refused\n"}}},
			1, []string{"<tool>: new-host: docker not reachable over ssh (is it installed, and is the user in the docker group?)"}},
		// shift 2 fails with one argument, so the flag loop sees the org.
		{Scenario{Name: "migrate, one argument", Args: []string{"migrate", "acme"}, Files: twoOrgs}, 1, []string{"<tool>: unknown flag acme"}},
		{Scenario{Name: "migrate, no arguments", Args: []string{"migrate"}, Files: twoOrgs}, 1, []string{"missing <org>"}},
		{Scenario{Name: "migrate, unknown flag", Args: []string{"migrate", "acme", "h", "--frob"}, Files: twoOrgs}, 1, []string{"<tool>: unknown flag --frob"}},
		{Scenario{Name: "migrate, unknown org", Args: []string{"migrate", "nope", "h"}, Files: twoOrgs}, 1, []string{"unknown org 'nope'"}},
	}
	scenarios = append(scenarios, more...)
	for _, s := range more {
		ported[s.Name] = true
	}
}

func withoutKey(m map[string]string, k string) map[string]string {
	out := map[string]string{}
	for key, v := range m {
		if key != k {
			out[key] = v
		}
	}
	return out
}

// TestBerthRestoreOwnsTheOrg: a restored org is berth's. A ccenv backup (no MANAGER) gets
// MANAGER=berth as its first line, another MANAGER value is replaced, and --force won't replace an
// org berth doesn't own (PARITY.md).
func TestBerthRestoreOwnsTheOrg(t *testing.T) {
	restore := func(env string, args ...string) Result {
		return run(t, BerthUnowned(berthBin), Scenario{Args: append([]string{"restore", "b.tar.zst", "--no-start"}, args...), Files: backupFiles,
			Rules: []Rule{extractRule(extracted("t-restored", env))}})
	}
	envOf := func(r Result) string {
		for _, l := range r.Tree {
			if s, ok := strings.CutPrefix(l, "state/orgs/t-restored/org.env 0600 "); ok {
				return s
			}
		}
		return ""
	}
	if r := restore(restoredEnv); r.Exit != 0 || !strings.HasPrefix(envOf(r), `"MANAGER=berth\nCLAUDE_CODE_OAUTH_TOKEN=`) {
		t.Errorf("ccenv backup: exit %d, stderr %q, org.env %s", r.Exit, r.Stderr, envOf(r))
	}
	if r := restore("MANAGER=ccenv\n" + restoredEnv); r.Exit != 0 || !strings.HasPrefix(envOf(r), `"MANAGER=berth\nCLAUDE_CODE_OAUTH_TOKEN=`) {
		t.Errorf("MANAGER=ccenv backup: exit %d, stderr %q, org.env %s", r.Exit, r.Stderr, envOf(r))
	}
	r := run(t, BerthUnowned(berthBin), Scenario{Args: []string{"restore", "b.tar.zst", "--force"}, Files: backupFiles,
		Rules: []Rule{extractRule(extracted("acme", acmeEnv))}})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "org 'acme' is managed by ccenv") || slices.ContainsFunc(r.Tree, func(l string) bool { return strings.Contains(l, ".replaced") }) {
		t.Errorf("--force over a ccenv org: exit %d, stderr %q", r.Exit, r.Stderr)
	}
	if !slices.ContainsFunc(r.Calls, func(c string) bool { return strings.Contains(c, `"--entrypoint" "rm"`) }) {
		t.Errorf("the staging dir wasn't cleaned up: %v", r.Calls)
	}
}
