//go:build integration

package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestHostSchedule: `schedule --host` puts berth and the operator's public recipient on the sshd +
// dind host (cron there: no systemd); the job it installed makes an age backup that only the
// operator's key restores; no private key is on the host; the job keeps working after berth on the
// host is upgraded (#49).
func TestHostSchedule(t *testing.T) {
	bin := os.Getenv("BERTH_BIN")
	if bin == "" {
		t.Skip("BERTH_BIN not set (the integration workflow builds berth and sets it)")
	}
	target := startSSHTarget(t)
	const fixture, o = "berth-t-sshd", "t-sched"
	home := t.TempDir()
	env := append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, "cfg"),
		"BERTH_HOME="+filepath.Join(home, "state"), "SSH_AUTH_SOCK=", "BERTH_PUBLISHED_IMAGE="+publishedImage)
	berthEnv := func(extra []string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(append([]string{}, env...), extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	berth := func(args ...string) (string, error) { return berthEnv(nil, args...) }
	must := func(args ...string) string {
		t.Helper()
		out, err := berth(args...)
		if err != nil {
			t.Fatalf("berth %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	asOps := func(script string) (string, error) {
		b, err := exec.Command("docker", "exec", "-u", "ops", "-e", "HOME=/home/ops", fixture, "sh", "-c", script).CombinedOutput()
		return string(b), err
	}

	out, _ := berth("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--no-guard")
	m := regexp.MustCompile(`--fingerprint (SHA256:\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no fingerprint offered:\n%s", out)
	}
	must("host", "add", "box", "ops@"+target.addr, "--identity", target.keyFile, "--fingerprint", m[1], "--no-guard")

	// Without a key here, there's nothing to encrypt to.
	if out, err := berth("schedule", "--host", "box"); err == nil || !strings.Contains(out, "keygen") {
		t.Fatalf("schedule without a key: %v\n%s", err, out)
	}
	_ = exec.Command("docker", "rmi", "-f", strings.TrimSpace(must("image-tag"))).Run() // the stub (TestRemoteLifecycle)
	pub := regexp.MustCompile(`age1[0-9a-z]+`).FindString(must("keygen"))
	if pub == "" {
		t.Fatal("keygen printed no public key")
	}

	// An org on box, and its image there (the scheduled job uses it).
	must("init", o+"@box", "--name", "Test User", "--email", "test@example.com")
	must("up", o+"@box")
	must("down", o+"@box")

	out = must("schedule", "--host", "box", "--at", "04:15", "--keep", "3")
	if !strings.Contains(out, "Scheduled (cron)") || !strings.Contains(out, pub) {
		t.Fatalf("schedule --host:\n%s", out)
	}
	if out := must("schedule", "--host", "box", "status"); !strings.Contains(out, "berth-backup") {
		t.Errorf("schedule --host status:\n%s", out)
	}
	table, err := asOps("crontab -l")
	mustDo(t, err)
	job := regexp.MustCompile(`(?m)^15 4 \* \* \* (.*)  # berth-backup$`).FindStringSubmatch(table)
	if job == nil || !strings.Contains(job[1], "BERTH_BACKUP_RECIPIENTS="+pub) || !strings.Contains(job[1], "/home/ops/.local/bin/berth ") {
		t.Fatalf("crontab on box:\n%s", table)
	}

	// No private key anywhere on the host.
	if leak, _ := asOps("grep -rl AGE-SECRET-KEY /home/ops /etc/crontabs 2>/dev/null; find / -xdev -name backup.key 2>/dev/null"); strings.TrimSpace(leak) != "" {
		t.Fatalf("a private key on the host:\n%s", leak)
	}

	// Run the job as cron would, twice: before and after berth on the host is upgraded (the link
	// moves; the job calls the link).
	backups := func() []string {
		out, _ := asOps("ls /home/ops/.local/share/berth/backups/ 2>/dev/null | grep '^" + o + "-.*\\.age$'")
		return strings.Fields(out)
	}
	if out, err := asOps(job[1]); err != nil || len(backups()) != 1 {
		logs, _ := asOps("cat /home/ops/.local/share/berth/backups/cron.log")
		t.Fatalf("the scheduled job: %v\n%s\n%s", err, out, logs)
	}
	if out, err := asOps(`set -e; d=~/.local/opt/berth/next; mkdir -p $d; cp ~/.local/opt/berth/*/berth $d/berth.new; mv $d/berth.new $d/berth; $d/berth install; readlink ~/.local/bin/berth`); err != nil || !strings.Contains(out, "/next/berth") {
		t.Fatalf("upgrading berth on box: %v\n%s", err, out)
	}
	time.Sleep(time.Second) // backup names have a one-second stamp
	if out, err := asOps(job[1]); err != nil || len(backups()) != 2 {
		t.Fatalf("the scheduled job after the upgrade: %v\n%s (backups: %v)", err, out, backups())
	}

	// Only the operator's key restores it.
	name := backups()[0]
	data, err := exec.Command("docker", "exec", fixture, "cat", "/home/ops/.local/share/berth/backups/"+name).Output()
	mustDo(t, err)
	local := filepath.Join(t.TempDir(), name)
	mustDo(t, os.WriteFile(local, data, 0o600))
	// Into a fresh state root (no orgs/ yet), as on a new machine.
	if out, err := berth("restore", local, "--as", "t-sched-r", "--no-start", "--no-rehydrate"); err != nil {
		logs, _ := asOps("ls -l /home/ops/.local/share/berth/backups/; cat /home/ops/.local/share/berth/backups/cron.log")
		key := filepath.Join(home, "cfg", "berth", "backup.key")
		listing, _ := exec.Command("docker", "run", "--rm", "-i", "-v", key+":/k:ro", "--entrypoint", "sh", strings.TrimSpace(must("image-tag")),
			"-c", "age -d -i /k | zstd -dc | tar -tv | head -20").CombinedOutput()
		t.Fatalf("restore: %v\n%s\non the host:\n%s\nthe archive, decrypted here:\n%s", err, out, logs, listing)
	}
	if _, err := os.Stat(filepath.Join(home, "state", "orgs", "t-sched-r", "org.env")); err != nil {
		t.Errorf("the restored org: %v", err)
	}
	other := filepath.Join(t.TempDir(), "other.key")
	if _, err := berthEnv([]string{"BERTH_BACKUP_KEY=" + other}, "keygen"); err != nil {
		t.Fatal(err)
	}
	if out, err := berthEnv([]string{"BERTH_BACKUP_KEY=" + other}, "restore", local, "--as", "t-sched-x", "--no-start", "--no-rehydrate"); err == nil {
		t.Fatalf("another key restored the host's backup:\n%s", out)
	} else if _, serr := os.Stat(filepath.Join(home, "state", "orgs", "t-sched-x")); serr == nil {
		t.Errorf("a failed restore left t-sched-x behind:\n%s", out)
	} else {
		t.Logf("another key, refused as it should be:\n%s", out)
	}

	out = must("schedule", "--host", "box", "off")
	if table, _ := asOps("crontab -l"); strings.Contains(table, "berth-backup") {
		t.Errorf("schedule --host off left the job:\n%s\n%s", table, out)
	}
}
