package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/ar4mirez/berth/internal/host"
)

// Backups are made by image/archive.sh inside berth's image, as root and with no network (plan,
// decision 4): sshd/*_key files are root-owned 0600, so the user process can't read them. berth only
// orchestrates, as ccenv does, with the same docker argv.

// backupEnv is a backup setting from the environment: BERTH_<key>, else ccenv's CCENV_<key>
// (plan: the CCENV_* backup variables stay accepted as aliases).
func (a *App) backupEnv(key string) string {
	if v := a.Getenv("BERTH_" + key); v != "" {
		return v
	}
	return a.Getenv("CCENV_" + key)
}

// backupsDir is where backups go by default: $BERTH_BACKUP_DIR, else <state>/backups (ccenv's
// <root>/backups, which is the same place once the state root is the legacy checkout).
func (a *App) backupsDir() string {
	if v := a.backupEnv("BACKUP_DIR"); v != "" {
		return v
	}
	return path.Join(a.State.Home.Path, "backups")
}

// keyFile is the age identity for key-based backups: $BERTH_BACKUP_KEY, else
// $XDG_CONFIG_HOME/berth/backup.key, falling back to ccenv's $XDG_CONFIG_HOME/ccenv/backup.key while
// berth has none, so the existing key keeps working after cutover (PARITY.md).
func (a *App) keyFile() string {
	if v := a.backupEnv("BACKUP_KEY"); v != "" {
		return v
	}
	cfg := a.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = a.Getenv("HOME") + "/.config"
	}
	own := cfg + "/berth/backup.key"
	if legacy := cfg + "/ccenv/backup.key"; !a.exists(own) && a.exists(legacy) {
		return legacy
	}
	return own
}

// exists is `[ -e p ]` (symlinks followed).
func (a *App) exists(p string) bool {
	_, err := a.Host.FS.Stat(p)
	return err == nil
}

var agePublic = regexp.MustCompile(`age1[0-9a-z]*`)

// publicKey is `$(grep -o 'age1[0-9a-z]*' "$f")`: every match, one per line.
func (a *App) publicKey(f string) (string, bool) {
	b, err := a.Host.FS.ReadFile(f)
	if err != nil {
		return "", false
	}
	var keys []string
	for _, l := range fileLines(b) {
		keys = append(keys, agePublic.FindAllString(l, -1)...)
	}
	return strings.Join(keys, "\n"), len(keys) > 0
}

// quiet runs a command with its output discarded (`>/dev/null 2>&1`).
func (a *App) quietRun(ctx context.Context, args ...string) error {
	return a.Host.Exec.Run(ctx, host.Cmd{Args: args, Stdout: io.Discard, Stderr: io.Discard})
}

// ensureImage is ensure_image: berth's image, with age and gpg in it, else a build.
func (a *App) ensureImage(ctx context.Context) error {
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	a.imageFromRelease(ctx) // a release pulls its image rather than building it
	if a.quietRun(ctx, "docker", "image", "inspect", set.Tag) == nil &&
		a.quietRun(ctx, "docker", "run", "--rm", "--entrypoint", "sh", set.Tag, "-c", "command -v age && command -v gpg") == nil {
		return nil
	}
	return a.Build(ctx, nil)
}

// Keygen is `ccenv keygen`: an age key for backups with no passphrase prompts (good for cron).
func (a *App) Keygen(ctx context.Context) error {
	kf := a.keyFile()
	if a.exists(kf) {
		pub, _ := a.publicKey(kf)
		return fmt.Errorf("%s already exists (public key: %s)", kf, pub)
	}
	if err := a.ensureImage(ctx); err != nil {
		return err
	}
	dir := path.Dir(kf)
	if err := a.Host.FS.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := a.Host.FS.Chmod(dir, 0o700); err != nil {
		return err
	}
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	// (umask 077; docker run … age-keygen 2>/dev/null > "$KEYFILE"): the file exists (0600) before
	// docker runs, and stays, empty, if it fails.
	w, err := a.Host.FS.Create(kf, 0o600)
	if err != nil {
		return err
	}
	err = a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "run", "--rm", "--network", "none", "--entrypoint", "age-keygen", set.Tag},
		Stdout: w, Stderr: io.Discard})
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	pub, _ := a.publicKey(kf)
	fmt.Fprintf(a.Stdout, "Created %s\nPublic key: %s\n\n", kf, pub)
	fmt.Fprintln(a.Stdout, "Backups now encrypt to this key automatically (no prompts).")
	fmt.Fprintf(a.Stdout, "IMPORTANT: store a copy of %s somewhere safe (e.g. your password manager).\n", kf)
	fmt.Fprintf(a.Stdout, "Without it, key-encrypted backups cannot be restored. Copy it to a new machine before '%s restore'.\n", Tool)
	return nil
}

// secrets is ccenv's SECRETS: a private temp dir on the host (0700), mounted read-only into the
// backup engine. Secrets never go through argv, the environment or the backup dir.
type secrets struct {
	a   *App
	dir string
}

func (s *secrets) path(name string) (string, error) {
	if s.dir == "" {
		d, err := s.a.Host.FS.MkdirTemp("", "tmp.XXXXXXXXXX")
		if err != nil {
			return "", err
		}
		s.dir = d
	}
	return s.dir + "/" + name, nil
}

// cleanup is the EXIT trap's `rm -rf "$SECRETS"`.
func (s *secrets) cleanup() {
	if s.dir != "" {
		_ = s.a.Host.FS.RemoveAll(s.dir)
	}
}

// appendTo is `… >> "$SECRETS/name"`: a new file gets 0666 minus the umask, as in ccenv (only
// the passphrase is written under umask 077; the recipients are public keys).
func (s *secrets) appendTo(ctx context.Context, name string, data []byte) error {
	p, err := s.path(name)
	if err != nil {
		return err
	}
	b, _ := s.a.Host.FS.ReadFile(p)
	return s.a.Host.FS.WriteFile(p, append(b, data...), 0o666&^s.a.umask(ctx))
}

// setPassphrase is set_passphrase: from $BERTH_BACKUP_PASSPHRASE (or CCENV_…), else prompted on
// /dev/tty; confirm asks twice and wants 12 characters.
func (a *App) setPassphrase(sec *secrets, confirm bool) error {
	p := a.backupEnv("BACKUP_PASSPHRASE")
	if p == "" {
		tty, err := a.openTTY()
		if err != nil {
			return fmt.Errorf("no passphrase: set BERTH_BACKUP_PASSPHRASE, or use a key (%s keygen)", Tool)
		}
		defer func() { _ = tty.Close() }()
		if p, err = a.readHidden(tty, "Backup passphrase: "); err != nil {
			return err
		}
		if confirm {
			if utf8.RuneCountInString(p) < 12 {
				return fmt.Errorf("use at least 12 characters (or a key: %s keygen)", Tool)
			}
			again, err := a.readHidden(tty, "Repeat passphrase: ")
			if err != nil {
				return err
			}
			if again != p {
				return errors.New("passphrases differ")
			}
		}
	}
	if p == "" {
		return errors.New("empty passphrase")
	}
	if confirm && utf8.RuneCountInString(p) < 12 {
		fmt.Fprintln(a.Stderr, "warning: passphrase shorter than 12 characters")
	}
	f, err := sec.path("pass")
	if err != nil {
		return err
	}
	return a.Host.FS.WriteFile(f, []byte(p), 0o600)
}

// openTTY opens the operator's terminal (ccenv's /dev/tty), or fails when there is none.
func (a *App) openTTY() (*os.File, error) {
	if a.OpenTTY == nil {
		return nil, errors.New("no terminal")
	}
	return a.OpenTTY()
}

// readHidden is `read -r -s -p "<prompt>" v </dev/tty; echo >&2`.
func (a *App) readHidden(tty *os.File, prompt string) (string, error) {
	fmt.Fprint(a.Stderr, prompt)
	b, err := term.ReadPassword(int(tty.Fd()))
	fmt.Fprintln(a.Stderr)
	return string(b), err
}

// addRecipient is add_recipient: an age1… or ssh- public key, or a file holding such lines.
func (a *App) addRecipient(ctx context.Context, sec *secrets, r string) error {
	if fi, err := a.Host.FS.Stat(r); err == nil && fi.Mode().IsRegular() {
		b, _ := a.Host.FS.ReadFile(r)
		var keys []byte
		for _, l := range fileLines(b) {
			if strings.HasPrefix(l, "age1") || strings.HasPrefix(l, "ssh-") {
				keys = append(keys, l+"\n"...)
			}
		}
		if len(keys) == 0 {
			return fmt.Errorf("no age/ssh public key in %s", r)
		}
		return sec.appendTo(ctx, "recipients", keys)
	}
	if !strings.HasPrefix(r, "age1") && !strings.HasPrefix(r, "ssh-") {
		return fmt.Errorf("not a recipient: %s", r)
	}
	return sec.appendTo(ctx, "recipients", []byte(r+"\n"))
}

// archive is ccenv's archive(): run image/archive.sh in berth's image as root, with no network,
// the org dir (or whatever mount says) mounted, and the secrets dir read-only if there is one.
func (a *App) archive(ctx context.Context, sec *secrets, orgName, enc, mount string, stdin io.Reader, stdout io.Writer, args ...string) error {
	set, _, err := a.composeAssets()
	if err != nil {
		return err
	}
	src, _ := a.capture(ctx, false, "hostname")
	if enc == "" {
		enc = "none"
	}
	argv := []string{"docker", "run", "--rm", "-i", "--network", "none", "--entrypoint", "bash",
		"-e", "ORG=" + orgName, "-e", "SRC_HOST=" + src, "-e", "ENC=" + enc}
	if sec.dir != "" {
		argv = append(argv, "-v", sec.dir+":/secrets:ro")
	}
	argv = append(argv, "-v", set.ImageDir+"/archive.sh:/archive.sh:ro", "-v", mount, set.Tag, "/archive.sh")
	return a.Host.Exec.Run(ctx, host.Cmd{Args: append(argv, args...), Stdin: stdin, Stdout: stdout, Stderr: a.Stderr})
}

var keepN = regexp.MustCompile(`^[1-9][0-9]*$`)

// Backup is `ccenv backup <org>...|--all [--plan] [-o file|dir|-] [--passphrase | -r <key> |
// --no-encrypt] [--keep N]`. --all is every berth-owned org (ccenv's skips berth's; each tool backs up
// its own). An org named explicitly can be either tool's: a backup doesn't change the org.
func (a *App) Backup(ctx context.Context, args []string) error {
	var orgs, recips []string
	out, mode, plan, keep := "", "", false, 0
	for i := 0; i < len(args); {
		x := args[i]
		switch x {
		case "--all":
			for _, d := range a.orgDirs() {
				if a.isFile(a.Orgs.EnvPath(d)) && a.env(d, "MANAGER") == "berth" {
					orgs = append(orgs, d)
				}
			}
		case "-o", "--output", "--recipient", "-r":
			// out="$2"; shift 2: under set -u a missing value ends the command (PARITY.md).
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", x)
			}
			if x == "-o" || x == "--output" {
				out = nth(args, i+1)
			} else {
				recips, mode = append(recips, nth(args, i+1)), "age"
			}
			i++
		case "--encrypt": // the default; kept for compatibility
		case "--passphrase":
			mode = "gpg"
		case "--no-encrypt":
			mode = "none"
		case "--plan", "--dry-run":
			plan = true
		case "--keep":
			k := nth(args, i+1)
			if !keepN.MatchString(k) {
				return errors.New("--keep needs a number >= 1")
			}
			if _, err := fmt.Sscan(k, &keep); err != nil {
				return errors.New("--keep needs a number >= 1")
			}
			i++
		default:
			if strings.HasPrefix(x, "-") {
				return fmt.Errorf("unknown flag %s", x)
			}
			orgs = append(orgs, x)
		}
		i++
	}
	if len(orgs) == 0 {
		return fmt.Errorf("usage: %s backup <org>...|--all [--plan] [-o file|dir|-] [--passphrase | -r <key> | --no-encrypt]", Tool)
	}
	if len(orgs) > 1 && out != "" && !a.isDir(out) {
		return errors.New("-o must be a directory when backing up several orgs")
	}
	if err := a.ensureImage(ctx); err != nil {
		return err
	}

	sec := &secrets{a: a}
	defer sec.cleanup()
	if !plan {
		// Encryption is on by default. Order: explicit flag > $BERTH_BACKUP_RECIPIENTS >
		// $BERTH_BACKUP_PASSPHRASE > key file > prompt.
		if mode == "" {
			kf := a.keyFile()
			switch {
			case a.backupEnv("BACKUP_RECIPIENTS") != "":
				mode = "age"
				recips = append(recips, strings.Fields(strings.ReplaceAll(a.backupEnv("BACKUP_RECIPIENTS"), ",", " "))...)
			case a.backupEnv("BACKUP_PASSPHRASE") != "":
				mode = "gpg"
			case a.isFile(kf):
				// recips+=("$(grep -o 'age1…' "$KEYFILE")"): with no key in the file, grep fails in
				// an assignment and set -e ends the command.
				pub, ok := a.publicKey(kf)
				if !ok {
					return &Exit{Code: 1}
				}
				mode, recips = "age", append(recips, pub)
			default:
				mode = "gpg"
			}
		}
		switch mode {
		case "gpg":
			if err := a.setPassphrase(sec, true); err != nil {
				return err
			}
		case "age":
			for _, r := range recips {
				if err := a.addRecipient(ctx, sec, r); err != nil {
					return err
				}
			}
		case "none":
			fmt.Fprintln(a.Stderr, "WARNING: --no-encrypt: the backup will contain the Claude token, git/ssh keys and passwords in plain form.")
		}
	}
	sfx := ".tar.zst"
	switch mode {
	case "gpg":
		sfx += ".gpg"
	case "age":
		sfx += ".age"
	}

	for _, o := range orgs {
		if err := a.needOrg(o); err != nil {
			return err
		}
		mount := path.Join(a.Orgs.Dir, o) + ":/src:ro"
		if plan {
			fmt.Fprintln(a.Stdout, "== "+o)
			if err := a.archive(ctx, sec, o, "", mount, a.Stdin, a.Stdout, "plan"); err != nil {
				return err
			}
			fmt.Fprintln(a.Stdout)
			continue
		}
		if out == "-" {
			if err := a.archive(ctx, sec, o, mode, mount, a.Stdin, a.Stdout, "create"); err != nil {
				return err
			}
			continue
		}
		toDir := out == "" || a.isDir(out)
		f := out
		if toDir {
			dir := out
			if dir == "" {
				dir = a.backupsDir()
			}
			if err := a.Host.FS.MkdirAll(dir, 0o777&^a.umask(ctx)); err != nil {
				return err
			}
			_ = a.Host.FS.Chmod(dir, 0o700)
			stamp, err := a.capture(ctx, false, "date", "+%Y%m%d-%H%M%S")
			if err != nil {
				return err
			}
			f = dir + "/" + o + "-" + stamp + sfx
		}
		fmt.Fprintln(a.Stderr, "== "+o)
		// archive plan | sed -n '1,3p' >&2: the first three lines; under pipefail a failing plan
		// ends the command.
		var planOut bytes.Buffer
		perr := a.archive(ctx, sec, o, "", mount, a.Stdin, &planOut, "plan")
		_, _ = a.Stderr.Write(headLines(planOut.Bytes(), 3))
		if perr != nil {
			return perr
		}
		if err := a.createBackup(ctx, sec, o, mode, mount, f); err != nil {
			_ = a.Host.FS.Remove(f)
			return fmt.Errorf("backup of %s failed", o)
		}
		var size int64
		if fi, err := a.Host.FS.Stat(f); err == nil {
			size = fi.Size()
		}
		kind := map[string]string{"gpg": "passphrase-encrypted", "age": "key-encrypted", "none": "NOT encrypted"}[mode]
		fmt.Fprintf(a.Stderr, "Wrote %s (%s, %s)\n", f, human(size), kind)
		if keep > 0 && toDir {
			dir := out
			if dir == "" {
				dir = a.backupsDir()
			}
			a.pruneBackups(dir, o, keep)
		}
	}
	return nil
}

// createBackup is `( umask 077; archive … create > "$f" )`.
func (a *App) createBackup(ctx context.Context, sec *secrets, o, mode, mount, f string) error {
	w, err := a.Host.FS.Create(f, 0o600)
	if err != nil {
		fmt.Fprintf(a.Stderr, "%s: %v\n", Tool, err)
		return err
	}
	err = a.archive(ctx, sec, o, mode, mount, a.Stdin, w, "create")
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	return err
}

// headLines is `sed -n '1,N p'`.
func headLines(b []byte, n int) []byte {
	end := 0
	for i := 0; i < n && end < len(b); i++ {
		j := bytes.IndexByte(b[end:], '\n')
		if j < 0 {
			return b
		}
		end += j + 1
	}
	return b[:end]
}

// pruneBackups is prune_backups: delete all but the newest keep backups of o in dir. The digit after
// the dash keeps "acme" from matching "acme-test".
func (a *App) pruneBackups(dir, o string, keep int) {
	re := regexp.MustCompile(`^` + o + `-[0-9]{8}-[0-9]{6}\.tar\.zst(\.gpg|\.age)?$`)
	entries, err := a.Host.FS.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && re.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) <= keep {
		return
	}
	for _, n := range names[keep:] {
		_ = a.Host.FS.Remove(dir + "/" + n)
		fmt.Fprintf(a.Stderr, "Pruned %s (keeping newest %d)\n", n, keep)
	}
}

// human is `numfmt --to=iec --suffix=B`: powers of 1024, rounded away from zero, with one decimal
// below 10.
func human(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	const units = "KMGTPEZY"
	v, p := float64(n), -1
	for v >= 1024 && p < len(units)-1 {
		v /= 1024
		p++
	}
	round := func(v float64) float64 {
		if v < 10 {
			return math.Ceil(v*10) / 10
		}
		return math.Ceil(v)
	}
	if v = round(v); v >= 1024 && p < len(units)-1 {
		v, p = round(v/1024), p+1
	}
	if v < 10 {
		return fmt.Sprintf("%.1f%cB", v, units[p])
	}
	return fmt.Sprintf("%.0f%cB", v, units[p])
}
