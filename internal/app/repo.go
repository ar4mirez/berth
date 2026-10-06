package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/host"
	"github.com/ar4mirez/berth/internal/ops"
	"github.com/ar4mirez/berth/internal/repopolicy"
)

// repoSubs are the subcommands ccenv's cmd_repo accepts (checked before anything else).
var repoSubs = map[string]bool{"add": true, "new": true, "create": true, "publish": true, "ls": true, "list": true,
	"rm": true, "remove": true, "adopt": true, "sync": true, "audit": true, "policy": true}

// repoWrites are the repo subcommands that change the org (policy too, when given a mode).
var repoWrites = map[string]bool{"add": true, "new": true, "create": true, "publish": true, "rm": true, "remove": true, "adopt": true, "sync": true}

// Repo is `ccenv repo <sub> <org> ...`. The writing subcommands need MANAGER=berth and are refused
// under --read-only; ls|list, audit and `policy` without a mode read.
func (a *App) Repo(ctx context.Context, sub, o string, rest []string) error {
	if !repoSubs[sub] {
		return fmt.Errorf("usage: %s repo add|new|publish|ls|rm|adopt|sync|audit|policy <org> ...", Tool) //nolint:staticcheck // ccenv's exact usage text
	}
	if repoWrites[sub] || (sub == "policy" && nth(rest, 0) != "") {
		if err := a.writable(o, "change "+o+"'s repos"); err != nil {
			return err
		}
	} else if err := a.needOrg(o); err != nil {
		return err
	}
	entries, err := a.repoEntries(ctx, o)
	if err != nil {
		return err
	}
	f := a.reposFile(o)
	switch sub {
	case "add":
		return a.repoAdd(ctx, o, f, rest)
	case "new", "create":
		return a.repoNew(ctx, o, f, entries, rest)
	case "publish":
		return a.repoPublish(ctx, o, f, entries, rest)
	case "rm", "remove":
		return a.repoRm(ctx, o, f, rest)
	case "adopt":
		return a.repoAdopt(ctx, o, f, rest)
	case "sync":
		return a.repoSync(ctx, o, entries)
	case "policy":
		return a.repoPolicy(ctx, o, nth(rest, 0))
	case "audit":
		return a.repoAudit(o, nth(rest, 0) == "--quiet")
	}
	return a.repoLs(ctx, o)
}

func (a *App) reposFile(o string) string { return path.Join(a.Orgs.Dir, o, "config", "repos.txt") }

// readEntries is repo_entries, read afresh: ccenv re-reads repos.txt on every call.
func (a *App) readEntries(f string) []repopolicy.Entry {
	data, err := a.Host.FS.ReadFile(f)
	if err != nil {
		return nil
	}
	return repopolicy.Entries(data)
}

// allowedDir is repo_allowed_dir.
func allowedDir(entries []repopolicy.Entry, dir string) bool {
	for _, e := range entries {
		if e.Dir == dir {
			return true
		}
	}
	return false
}

// canonicalSpec is repo_url's test for a spec that is already host/owner/repo.
var canonicalSpec = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+/[^/:]+/[^/:]+$`)

// repoURL is repo_url: owner/repo, host/owner/repo or any git URL as an ssh clone URL. A spec with
// no canonical form gives "git@:.git", as in ccenv.
func repoURL(spec string) string {
	c := spec
	if !canonicalSpec.MatchString(spec) {
		c = repopolicy.Canon(spec, "")
	}
	h, p, ok := strings.Cut(c, "/")
	if !ok {
		p = c // ${c#*/} leaves c alone when it has no '/'
	}
	return "git@" + h + ":" + p + ".git"
}

var (
	addCanon = regexp.MustCompile(`^[a-z0-9.-]+/[a-z0-9._-]+/[a-z0-9._/-]+$`)
	dirName  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// appendLine is `printf '%s\n' … >> f` (no newline added before it).
func (a *App) appendLine(f, line string) error {
	b, err := a.Host.FS.ReadFile(f)
	if err != nil && a.isFile(f) {
		return err
	}
	return a.Host.FS.WriteFile(f, append(b, line+"\n"...), 0o644)
}

// repoAdd is `ccenv repo add <org> <owner/repo|git-url> [--dir name] [--branch b] [--no-clone]`.
func (a *App) repoAdd(ctx context.Context, o, f string, args []string) error {
	spec, dir, branch, clone := "", "", "", true
	for i := 0; i < len(args); {
		switch x := args[i]; {
		case x == "--dir" || x == "--branch" || x == "-b":
			// dir="$2"; shift 2: under set -u a missing value ends the command (PARITY.md).
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", x)
			}
			if x == "--dir" {
				dir = nth(args, i+1)
			} else {
				branch = nth(args, i+1)
			}
			i += 2
		case x == "--no-clone":
			clone = false
			i++
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		default:
			spec = x
			i++
		}
	}
	if spec == "" {
		return fmt.Errorf("usage: %s repo add <org> <owner/repo|git-url> [--dir name] [--branch b] [--no-clone]", Tool)
	}
	canon, url := repopolicy.Canon(spec, ""), repoURL(spec)
	if !addCanon.MatchString(canon) {
		return fmt.Errorf("can't parse repo '%s'", spec)
	}
	if dir == "" {
		dir = path.Base(canon)
	}
	if !dirName.MatchString(dir) || dir == ".claude" {
		return fmt.Errorf("invalid dir name '%s'", dir)
	}
	// The registration runs under the state root's lock (re-reading repos.txt); the clone doesn't.
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	entries := a.readEntries(f)
	// existing=$(repo_entries | awk -v d="$dir" '$1==d {print $2}'): every match, one per line,
	// "-" for a URL with no canonical form.
	var ex []string
	for _, e := range entries {
		if e.Dir == dir {
			c := e.Canon
			if c == "" {
				c = "-"
			}
			ex = append(ex, c)
		}
	}
	existing := strings.Join(ex, "\n")
	if existing != "" && existing != canon {
		return fmt.Errorf("/workspace/%s is already registered for %s", dir, existing)
	}
	if existing == "" {
		line := dir + " " + url
		if branch != "" {
			line += " " + branch
		}
		if err := a.appendLine(f, line); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Registered %s as /workspace/%s\n", canon, dir)
	} else {
		fmt.Fprintf(a.Stdout, "%s is already registered as /workspace/%s\n", canon, dir)
	}
	unlock()
	if !clone {
		return nil
	}
	if !a.running(ctx, o) {
		fmt.Fprintf(a.Stdout, "(container stopped; it will be cloned by: %s repo sync %s)\n", Tool, o)
		return nil
	}
	if a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "test", "-e", "/workspace/"+dir) == nil {
		fmt.Fprintf(a.Stdout, "/workspace/%s already present\n", dir)
		a.warnIfReadOnly(ctx, o, dir, canon)
		return nil
	}
	if a.cloneIn(ctx, o, branch, url, dir) != nil {
		if existing == "" {
			// grep -v "^$dir " "$f" > "$tmp"; cat "$tmp" > "$f": dir is a regexp here (a '.' matches
			// any character). When nothing is left, grep exits 1 and set -e ends the command.
			relock, err := a.lock(ctx)
			if err != nil {
				return err
			}
			defer relock()
			re := regexp.MustCompile("^" + dir + " ")
			b, _ := a.Host.FS.ReadFile(f)
			var kept []byte
			for _, l := range fileLines(b) {
				if !re.MatchString(l) {
					kept = append(kept, l+"\n"...)
				}
			}
			if len(kept) == 0 {
				return &Exit{Code: 1}
			}
			if err := a.Host.FS.WriteFile(f, kept, 0o644); err != nil {
				return err
			}
		}
		return fmt.Errorf("clone failed; registration rolled back. Does this org's key have access? (%s info %s)", Tool, o)
	}
	fmt.Fprintf(a.Stdout, "Cloned into /workspace/%s\n", dir)
	a.warnIfReadOnly(ctx, o, dir, canon)
	return nil
}

// pushRefused is what a git host says when the credential can't write to the repo: a read-only or
// another repo's deploy key over SSH, a token without push access over HTTPS.
var pushRefused = regexp.MustCompile(`(?i)denied|read.only|permission|\b403\b|not authori[sz]ed|write access`)

// warnIfReadOnly warns when the org can't push to the repo it just registered (#103; berth-only,
// PARITY.md). A clone of a public repo needs no write access, so without this the first sign is a
// failed push from inside the container. It asks the repo's own remote with a dry-run push, over
// whichever transport the org uses, and creates nothing. Read-only use is legitimate, so this only
// warns; a failure that isn't a refusal (an empty repo, no network) says nothing.
func (a *App) warnIfReadOnly(ctx context.Context, o, dir, canon string) {
	var stderr bytes.Buffer
	err := a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"docker", "exec", "-u", "node", "-w", "/workspace/" + dir, "claude-" + o,
		"git", "push", "--dry-run", "--quiet", "origin", "HEAD:refs/heads/berth-write-check"}, Stdout: io.Discard, Stderr: &stderr})
	if err == nil || !pushRefused.Match(stderr.Bytes()) {
		return
	}
	said := ""
	for _, l := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		if pushRefused.MatchString(l) {
			said = strings.TrimSpace(l)
			break
		}
	}
	how, _ := a.capture(ctx, true, "docker", "exec", "claude-"+o, contract.GitTransport, "show")
	fmt.Fprintf(a.Stderr, "WARNING: %s can clone %s but can't push to it: %s\n", o, canon, said)
	if how == "https" {
		fmt.Fprintf(a.Stderr, `  git uses the gh login's token there, and that account has no write access to the repo.
  Fix: give the account write access, or sign in with one that has it: %[1]s gh-login %[2]s --force
`, Tool, o)
		return
	}
	fmt.Fprintf(a.Stderr, `  git uses the org's SSH key there. A key added as a deploy key writes to one repo only.
  Fix: %[1]s gh-login %[2]s (git then uses that login's token for every registered repo),
       or add the key to the GitHub account instead of as a deploy key (%[1]s info %[2]s prints it).
`, Tool, o)
}

// cloneIn clones url into /workspace/<dir> inside the container, as the node user.
func (a *App) cloneIn(ctx context.Context, o, branch, url, dir string) error {
	argv := []string{"docker", "exec", "-u", "node", "-w", "/workspace", "claude-" + o, "git", "clone"}
	if branch != "" {
		argv = append(argv, "--branch", branch)
	}
	return a.passthrough(ctx, false, append(argv, url, dir)...)
}

var (
	// newCanonical is repo new's test for a spec already in host/owner/repo form (any case).
	newCanonical = regexp.MustCompile(`^[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+/[^/:]+/[^/:]+$`)
	githubCanon  = regexp.MustCompile(`^github\.com/[a-z0-9_.-]+/[a-z0-9_.-]+$`)
)

// repoNew is `ccenv repo new|create <org> <owner/repo> [--private|--public|--internal] [-d desc]
// [--template o/r] [--readme] [--gitignore g] [--license l] [--dir d]`, or `<name> --local`: a
// brand-new repo, created on GitHub (with the host's gh, else the container's) or locally, then
// registered.
func (a *App) repoNew(ctx context.Context, o, f string, entries []repopolicy.Entry, args []string) error {
	var spec, dir, desc, tpl, gi, lic string
	vis, readme, localOnly := "--private", false, false
	values := map[string]*string{"-d": &desc, "--description": &desc, "--template": &tpl, "-p": &tpl,
		"--gitignore": &gi, "-g": &gi, "--license": &lic, "-l": &lic, "--dir": &dir}
	for i := 0; i < len(args); {
		x := args[i]
		switch v, ok := values[x]; {
		case ok:
			// desc="$2"; shift 2: under set -u a missing value ends the command (PARITY.md).
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", x)
			}
			*v = args[i+1]
			i += 2
			continue
		case x == "--private" || x == "--public" || x == "--internal":
			vis = x
		case x == "--readme" || x == "--add-readme":
			readme = true
		case x == "--local":
			localOnly = true
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		default:
			spec = x
		}
		i++
	}
	if spec == "" {
		return fmt.Errorf("usage: %[1]s repo new <org> <owner/repo> [--private|--public|--internal] [-d desc] [--template o/r] [--readme] [--gitignore Node] [--license mit] [--dir d]\n"+
			"       %[1]s repo new <org> <name> --local        (no GitHub repo yet; publish later with: %[1]s repo publish)", Tool)
	}

	if localOnly {
		if dir == "" {
			dir = spec[strings.LastIndex(spec, "/")+1:] // ${spec##*/}
		}
		if !dirName.MatchString(dir) || dir == ".claude" {
			return fmt.Errorf("invalid dir name '%s'", dir)
		}
		unlock, err := a.lock(ctx)
		if err != nil {
			return err
		}
		defer unlock()
		if allowedDir(a.readEntries(f), dir) {
			return fmt.Errorf("/workspace/%s is already registered", dir)
		}
		if err := a.needUp(ctx, o); err != nil {
			return err
		}
		if a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "test", "-e", "/workspace/"+dir) == nil {
			return fmt.Errorf("/workspace/%s already exists (register it with: %s repo adopt %s %s)", dir, Tool, o, dir)
		}
		if err := a.appendLine(f, dir+" local"); err != nil {
			return err
		}
		unlock()
		if err := a.passthrough(ctx, false, "docker", "exec", "-u", "node", "-w", "/workspace", "claude-"+o, "git", "init", "-q", "-b", "main", dir); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Created local repo /workspace/%s (no remote). Publish it later with: %s repo publish %s %s <owner/repo>\n", dir, Tool, o, dir)
		return nil
	}

	canon := repopolicy.Canon(spec, "")
	if newCanonical.MatchString(spec) {
		canon = strings.Map(func(r rune) rune { // tr '[:upper:]' '[:lower:]' (LC_ALL=C: ASCII only)
			if r >= 'A' && r <= 'Z' {
				return r + 'a' - 'A'
			}
			return r
		}, spec)
	}
	if !githubCanon.MatchString(canon) {
		return fmt.Errorf("repo new creates GitHub repos: use <owner/repo> (got '%s'). For a repo with no remote use --local.", spec) //nolint:staticcheck // ccenv's exact text
	}
	if repopolicy.Allowed(entries, canon) {
		return fmt.Errorf("%s is already registered", canon)
	}
	if dir == "" {
		dir = path.Base(canon)
	}
	if allowedDir(entries, dir) {
		return fmt.Errorf("/workspace/%s is already registered", dir)
	}
	name := strings.TrimPrefix(canon, "github.com/")
	ghArgs := []string{"repo", "create", name, vis}
	if desc != "" {
		ghArgs = append(ghArgs, "--description", desc)
	}
	if tpl != "" {
		ghArgs = append(ghArgs, "--template", tpl)
	}
	if readme {
		ghArgs = append(ghArgs, "--add-readme")
	}
	if gi != "" {
		ghArgs = append(ghArgs, "--gitignore", gi)
	}
	if lic != "" {
		ghArgs = append(ghArgs, "--license", lic)
	}
	// Created with the host's gh (outside the container), so the container's lock is never
	// loosened; the container's own gh login (gh-login) is the fallback.
	if a.hostGh(ctx) {
		if a.passthrough(ctx, true, append([]string{"gh"}, ghArgs...)...) != nil {
			return fmt.Errorf("GitHub refused to create %s (name taken, or no permission in that org?)", name)
		}
	} else {
		if err := a.needUp(ctx, o); err != nil {
			return err
		}
		if a.passthrough(ctx, true, append([]string{"docker", "exec", "-u", "node", "claude-" + o}, a.execLoader(o, append([]string{"gh"}, ghArgs...)...)...)...) != nil {
			return fmt.Errorf("couldn't create %s: no host gh, and the container's gh failed (%s gh-login %s?)", name, Tool, o)
		}
	}
	fmt.Fprintf(a.Stdout, "Created https://%s (%s)\n", canon, strings.TrimPrefix(vis, "--"))
	if tpl != "" {
		// Template repos are populated asynchronously.
		if err := a.passthrough(ctx, false, "sleep", "3"); err != nil {
			return err
		}
	}
	// cmd_repo add "$org" owner/repo --dir "$dir", which runs ensure_repos_file again (repoAdd then
	// re-reads repos.txt under the lock).
	if _, err := a.repoEntries(ctx, o); err != nil {
		return err
	}
	return a.repoAdd(ctx, o, f, []string{name, "--dir", dir})
}

// hostGh is `command -v gh >/dev/null && gh auth status >/dev/null 2>&1`: gh on the host and
// signed in.
func (a *App) hostGh(ctx context.Context) bool {
	return a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"gh", "auth", "status"}, Stdout: io.Discard, Stderr: io.Discard}) == nil
}

// repoPublish is `ccenv repo publish <org> <dir> <owner/repo> [--private|--public|--internal]`:
// push a --local repo to a new GitHub repo, then register that remote.
func (a *App) repoPublish(ctx context.Context, o, f string, entries []repopolicy.Entry, args []string) error {
	dir, spec, vis := nth(args, 0), nth(args, 1), "--private"
	// shift 2 2>/dev/null || true: with fewer than two arguments nothing is shifted, so the flag
	// loop sees the dir itself.
	flags := args
	if len(args) >= 2 {
		flags = args[2:]
	}
	for _, x := range flags {
		if x != "--private" && x != "--public" && x != "--internal" {
			return fmt.Errorf("unknown flag %s", x)
		}
		vis = x
	}
	if dir == "" || spec == "" {
		return fmt.Errorf("usage: %s repo publish <org> <dir> <owner/repo> [--private|--public|--internal]", Tool)
	}
	// [ "$(repo_entries | awk -v d="$dir" '$1==d {print $3}')" = local ]: every match's URL.
	var urls []string
	for _, e := range entries {
		if e.Dir == dir {
			urls = append(urls, e.URL)
		}
	}
	if strings.Join(urls, "\n") != "local" {
		return fmt.Errorf("/workspace/%s is not a local-only repo", dir)
	}
	if !a.hostGh(ctx) {
		return errors.New("publish needs gh signed in on the host")
	}
	src := path.Join(a.Orgs.Dir, o, "workspace", dir)
	canon := repopolicy.Canon(spec, "")
	if a.passthrough(ctx, true, "git", "-C", src, "rev-parse", "-q", "--verify", "HEAD") != nil {
		return fmt.Errorf("/workspace/%s has no commits yet; commit something first", dir)
	}
	name := strings.TrimPrefix(canon, "github.com/")
	if a.passthrough(ctx, true, "gh", "repo", "create", name, vis, "--source", src, "--remote", "origin", "--push") != nil {
		return errors.New("publishing failed (name taken, or no permission?)")
	}
	url := repoURL(canon)
	if err := a.passthrough(ctx, false, "git", "-C", src, "remote", "set-url", "origin", url); err != nil {
		return err
	}
	// awk -v d="$dir" -v u="$url" '$1==d {$2=u} {print}': a matching line is rebuilt from its
	// fields, joined by single spaces; the others are printed as they are. Under the lock.
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	b, _ := a.Host.FS.ReadFile(f)
	var out []byte
	for _, l := range fileLines(b) {
		if fs := strings.FieldsFunc(l, func(r rune) bool { return r == ' ' || r == '\t' }); len(fs) > 0 && fs[0] == dir {
			if len(fs) < 2 {
				fs = append(fs, "")
			}
			fs[1] = url
			l = strings.Join(fs, " ")
		}
		out = append(out, l+"\n"...)
	}
	if err := a.Host.FS.WriteFile(f, out, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Published /workspace/%s to https://%s (%s); it now pushes/pulls from there.\n", dir, canon, strings.TrimPrefix(vis, "--"))
	return nil
}

// repoRm is `ccenv repo rm <org> <dir> [--delete]` (--delete only as the 2nd argument).
func (a *App) repoRm(ctx context.Context, o, f string, args []string) error {
	dir, del := nth(args, 0), nth(args, 1)
	if dir == "" {
		return fmt.Errorf("usage: %s repo rm <org> <dir> [--delete]", Tool)
	}
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	entries := a.readEntries(f) // re-read under the lock
	if !allowedDir(entries, dir) {
		return fmt.Errorf("/workspace/%s is not registered", dir)
	}
	// awk -v d="$dir" '$1!=d': awk's first field (split on spaces and tabs); every kept line is
	// printed with a newline.
	b, _ := a.Host.FS.ReadFile(f)
	var kept []byte
	for _, l := range fileLines(b) {
		if awkField1(l) != dir {
			kept = append(kept, l+"\n"...)
		}
	}
	if err := a.Host.FS.WriteFile(f, kept, 0o644); err != nil {
		return err
	}
	unlock()
	fmt.Fprintf(a.Stdout, "Unregistered /workspace/%s\n", dir)
	if del == "--delete" && a.running(ctx, o) {
		if err := a.passthrough(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "rm", "-rf", "/workspace/"+dir); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Deleted /workspace/%s\n", dir)
	} else if a.running(ctx, o) {
		fmt.Fprintf(a.Stdout, "Its folder will be moved to orgs/%s/quarantine/ by the workspace sweep (within 30s).\n", o)
	}
	return nil
}

// awkField1 is awk's $1 with the default field separator.
func awkField1(l string) string {
	f := strings.FieldsFunc(l, func(r rune) bool { return r == ' ' || r == '\t' })
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// adoptURL is adopt_url: the registry URL for an existing folder (its origin, or "local" for a repo
// with no remote), found with git on the host; ok is false if it isn't a git repo.
func (a *App) adoptURL(ctx context.Context, dir string) (string, bool) {
	if u, err := a.capture(ctx, true, "git", "-C", dir, "remote", "get-url", "origin"); err == nil {
		return repoURL(u), true
	}
	if a.Host.Exec.Run(ctx, host.Cmd{Args: []string{"git", "-C", dir, "rev-parse", "--git-dir"}, Stdout: io.Discard, Stderr: io.Discard}) == nil {
		return "local", true
	}
	return "", false
}

// repoAdopt is `ccenv repo adopt <org> <dir>...|--all`: register folders already in the workspace,
// or restore them from quarantine.
func (a *App) repoAdopt(ctx context.Context, o, f string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: %s repo adopt <org> <dir>...   (or --all for everything unregistered)", Tool)
	}
	ws, qdir := path.Join(a.Orgs.Dir, o, "workspace"), path.Join(a.Orgs.Dir, o, "quarantine")
	unlock, err := a.lock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	names := args
	if args[0] == "--all" {
		names = nil
		entries := a.readEntries(f)
		for _, n := range a.workspaceGlob(ws) {
			if n != ".claude" && !allowedDir(entries, n) {
				names = append(names, n)
			}
		}
	}
	for _, n := range names {
		src := path.Join(ws, n)
		if _, err := a.Host.FS.Stat(src); err != nil {
			// q=$(ls -d "$quarantine/$n".* 2>/dev/null | sort | tail -1): with no match ls fails, and
			// under pipefail and set -e that ends the command with exit 2 (PARITY.md, legacy quirks).
			q := a.newestQuarantined(qdir, n)
			if q == "" {
				return &Exit{Code: 2}
			}
			url, ok := a.adoptURL(ctx, q)
			if !ok {
				fmt.Fprintf(a.Stdout, "skip %s: not a git repo\n", n)
				continue
			}
			if err := a.appendLine(f, n+" "+url); err != nil {
				return err
			}
			if err := a.Host.FS.Rename(q, src); err != nil {
				return err
			}
			fmt.Fprintf(a.Stdout, "Restored %s from quarantine and registered it (%s)\n", n, url)
			continue
		}
		url, ok := a.adoptURL(ctx, src)
		if !ok {
			fmt.Fprintf(a.Stdout, "skip %s: not a git repo\n", n)
			continue
		}
		if allowedDir(a.readEntries(f), n) {
			fmt.Fprintf(a.Stdout, "%s already registered\n", n)
			continue
		}
		if err := a.appendLine(f, n+" "+url); err != nil {
			return err
		}
		shown := "local only"
		if url != "local" {
			shown = repopolicy.Canon(url, "")
		}
		fmt.Fprintf(a.Stdout, "Registered %s -> %s\n", n, shown)
	}
	return nil
}

// newestQuarantined is `ls -d "$qdir/$n".* | sort | tail -1`: the last match in byte order
// (LC_ALL=C), or "".
func (a *App) newestQuarantined(qdir, n string) string {
	dir, base := path.Split(path.Join(qdir, n) + ".")
	entries, err := a.Host.FS.ReadDir(path.Clean(dir))
	if err != nil {
		return ""
	}
	var matches []string
	for _, e := range entries {
		if name := e.Name(); strings.HasPrefix(name, base) {
			matches = append(matches, dir+name)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[len(matches)-1]
}

// repoSync is `ccenv repo sync <org>`: clone anything registered but missing.
func (a *App) repoSync(ctx context.Context, o string, entries []repopolicy.Entry) error {
	if err := a.needUp(ctx, o); err != nil {
		return err
	}
	cloned := false
	for _, e := range entries {
		if a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "test", "-e", "/workspace/"+e.Dir) == nil {
			continue
		}
		if e.URL == "local" {
			fmt.Fprintf(a.Stdout, "SKIP %s: local-only repo is missing (restore it from a backup)\n", e.Dir)
			continue
		}
		cloned = true
		b := e.Branch
		if b == "-" {
			b = ""
		}
		if a.cloneIn(ctx, o, b, e.URL, e.Dir) == nil {
			fmt.Fprintf(a.Stdout, "Cloned %s\n", e.Dir)
		} else {
			fmt.Fprintf(a.Stdout, "FAILED %s\n", e.Dir)
		}
	}
	if !cloned {
		fmt.Fprintln(a.Stdout, "All registered repos are present.")
	}
	return nil
}

// repoPolicy is `ccenv repo policy <org> [enforce|warn|off]`.
func (a *App) repoPolicy(ctx context.Context, o, m string) error {
	if m == "" {
		pol, err := ops.GetRepoPolicy(a, o)
		if err != nil {
			return err
		}
		if a.Output == OutputJSON {
			return a.writeJSON(pol)
		}
		fmt.Fprintln(a.Stdout, "REPO_POLICY="+pol.Policy)
		return nil
	}
	if m != "enforce" && m != "warn" && m != "off" {
		return errors.New("policy must be enforce|warn|off")
	}
	if err := a.Orgs.Set(o, "REPO_POLICY", m); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "REPO_POLICY="+m)
	// running && { compose … >/dev/null 2>&1; echo …; }: when the org is stopped the function ends
	// on the failed test, and set -e exits 1 (PARITY.md, legacy quirks).
	if !a.running(ctx, o) {
		return &Exit{Code: 1}
	}
	q := *a
	q.Stdout, q.Stderr = io.Discard, io.Discard
	if err := q.compose(ctx, o, "up", "-d", "--force-recreate"); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "Restarted claude-%s to apply it.\n", o)
	return nil
}

// repoEntries is ensure_repos_file (write the header if repos.txt is missing; not under
// --read-only) plus repo_entries.
func (a *App) repoEntries(ctx context.Context, o string) ([]repopolicy.Entry, error) {
	f := a.reposFile(o)
	data, err := a.Host.FS.ReadFile(f)
	if err != nil && !a.isFile(f) {
		if !a.State.ReadOnly {
			header := "# Repos allowed in the '" + o + "' container. Managed with: " + Tool + " repo add|rm|adopt " + o + " ...\n" +
				"# <dir under /workspace>  <clone url>  [branch]\n"
			if err := a.writeNew(ctx, f, []byte(header)); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	return repopolicy.Entries(data), nil
}

func (a *App) repoLs(ctx context.Context, o string) error {
	// The audit can fail (no quarantine dir yet: exit 2) after the table is printed, as in ccenv.
	repos, err := ops.GetRepos(ctx, a, o)
	if a.Output == OutputJSON {
		if err != nil {
			return err
		}
		return a.writeJSON(repos)
	}
	fmt.Fprintf(a.Stdout, "%-22s %-40s %-18s %s\n", "DIR", "REPO", "BRANCH", "STATUS")
	for _, r := range repos.Repos {
		fmt.Fprintf(a.Stdout, "%-22s %-40s %-18s %s\n", r.Dir, r.RepoText, r.BranchText, r.StatusText)
	}
	if len(repos.Repos) == 0 {
		fmt.Fprintf(a.Stdout, "(no repos registered; add one: %s repo add %s <owner/repo>)\n", Tool, o)
	}
	if err != nil {
		return err
	}
	a.printAudit(o, repos.RepoAudit, true)
	return nil
}

// repoAudit is `ccenv repo audit <org> [--quiet]`: unregistered folders in the workspace, and what's
// in quarantine.
func (a *App) repoAudit(o string, quiet bool) error {
	au, err := ops.GetRepoAudit(a, o)
	if err != nil {
		return err
	}
	if a.Output == OutputJSON {
		return a.writeJSON(au)
	}
	a.printAudit(o, au.RepoAudit, quiet)
	return nil
}

// printAudit is the audit as ccenv prints it.
func (a *App) printAudit(o string, au ops.RepoAudit, quiet bool) {
	if len(au.Unregistered) > 0 {
		fmt.Fprintf(a.Stdout, "\nNot allowed in /workspace (policy: %s): %s\n", au.Policy, strings.Join(au.Unregistered, " "))
		fmt.Fprintf(a.Stdout, "  keep one -> %s repo adopt %s <dir>    (enforce mode quarantines them within 30s)\n", Tool, o)
	} else if !quiet {
		fmt.Fprintln(a.Stdout, "Workspace clean: only registered repos.")
	}
	if len(au.Quarantined) > 0 {
		fmt.Fprintf(a.Stdout, "\nQuarantined (%s):\n", au.QuarantineDir)
		for _, n := range au.Quarantined {
			fmt.Fprintln(a.Stdout, "  "+n)
		}
		fmt.Fprintf(a.Stdout, "  bring a repo back -> %s repo adopt %s <name>\n", Tool, o)
	}
}

// workspaceGlob is `for n in "$ws"/* "$ws"/.[!.]*; do [ -e "$n" ] || continue; …`: the names of
// visible entries, then of hidden ones whose second character isn't '.', each group sorted, with
// broken symlinks skipped ([ -e ] follows links).
func (a *App) workspaceGlob(ws string) []string {
	entries, err := a.Host.FS.ReadDir(ws)
	if err != nil {
		return nil
	}
	var visible, hidden []string
	for _, e := range entries {
		n := e.Name()
		if _, err := a.Host.FS.Stat(path.Join(ws, n)); err != nil {
			continue
		}
		switch {
		case !strings.HasPrefix(n, "."):
			visible = append(visible, n)
		case len(n) > 1 && n[1] != '.':
			hidden = append(hidden, n)
		}
	}
	sort.Strings(visible)
	sort.Strings(hidden)
	return append(visible, hidden...)
}

// OrgNames lists the orgs (dirs with an org.env) for shell completion.
func (a *App) OrgNames() []string {
	var names []string
	for _, d := range a.orgDirs() {
		if a.isFile(a.Orgs.EnvPath(d)) {
			names = append(names, d)
		}
	}
	return names
}
