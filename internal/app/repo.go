package app

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/ar4mirez/berth/internal/repopolicy"
)

// repoSubs are the subcommands ccenv's cmd_repo accepts (checked before anything else).
var repoSubs = map[string]bool{"add": true, "new": true, "create": true, "publish": true, "ls": true, "list": true,
	"rm": true, "remove": true, "adopt": true, "sync": true, "audit": true, "policy": true}

// Repo is `ccenv repo <sub> <org> ...` for the subcommands that only read: ls|list and audit.
func (a *App) Repo(ctx context.Context, sub, o string, rest []string) error {
	if !repoSubs[sub] {
		return fmt.Errorf("usage: %s repo add|new|publish|ls|rm|adopt|sync|audit|policy <org> ...", Tool) //nolint:staticcheck // ccenv's exact usage text
	}
	if sub != "ls" && sub != "list" && sub != "audit" {
		return fmt.Errorf("repo %s is not in berth yet (phase 3); use ccenv for now", sub)
	}
	if err := a.needOrg(o); err != nil {
		return err
	}
	entries, err := a.repoEntries(ctx, o)
	if err != nil {
		return err
	}
	if sub == "audit" {
		return a.repoAudit(ctx, o, entries, len(rest) > 0 && rest[0] == "--quiet")
	}
	return a.repoLs(ctx, o, entries)
}

// repoEntries is ensure_repos_file (write the header if repos.txt is missing; not under
// --read-only) plus repo_entries.
func (a *App) repoEntries(ctx context.Context, o string) ([]repopolicy.Entry, error) {
	f := path.Join(a.Orgs.Dir, o, "config", "repos.txt")
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

func (a *App) repoLs(ctx context.Context, o string, entries []repopolicy.Entry) error {
	fmt.Fprintf(a.Stdout, "%-22s %-40s %-18s %s\n", "DIR", "REPO", "BRANCH", "STATUS")
	for _, e := range entries {
		b := e.Branch
		var st string
		switch {
		case a.running(ctx, o) && a.passthrough(ctx, false, "docker", "exec", "claude-"+o, "test", "-d", "/workspace/"+e.Dir+"/.git") == nil:
			cur, n := a.repoDirty(ctx, o, e.Dir)
			st, b = "cloned", cur
			if n != "0" {
				st = "cloned, " + n + " changed"
			}
		case a.isDir(path.Join(a.Orgs.Dir, o, "workspace", e.Dir, ".git")):
			st = "cloned (container down)"
		default:
			st = "MISSING (" + Tool + " repo sync " + o + ")"
		}
		if b == "-" {
			b = ""
		}
		if b == "" {
			b = "default"
		}
		c := e.Canon
		if c == "" {
			c = "-" // repo_entries' placeholder for a URL with no canonical form
		}
		if e.URL == "local" {
			c = "(local only, not published)"
		}
		fmt.Fprintf(a.Stdout, "%-22s %-40s %-18s %s\n", e.Dir, c, b, st)
	}
	if len(entries) == 0 {
		fmt.Fprintf(a.Stdout, "(no repos registered; add one: %s repo add %s <owner/repo>)\n", Tool, o)
	}
	return a.repoAudit(ctx, o, entries, true)
}

// repoDirty is `read -r cur n <<<"$(repo_dirty …)"`: the first line of
// `docker exec … git branch/status … 2>/dev/null || echo "? ?"`, split into two words.
func (a *App) repoDirty(ctx context.Context, o, dir string) (string, string) {
	out, err := a.captureRaw(ctx, true, "docker", "exec", "-u", "node", "-w", "/workspace/"+dir, "claude-"+o, "sh", "-c",
		`printf "%s %s" "$(git branch --show-current 2>/dev/null || echo ?)" "$(git status --porcelain 2>/dev/null | wc -l)"`)
	if err != nil {
		out += "? ?\n"
	}
	first, _, _ := strings.Cut(out, "\n")
	f := strings.Fields(first)
	switch len(f) {
	case 0:
		return "", ""
	case 1:
		return f[0], ""
	}
	return f[0], strings.Join(f[1:], " ") // read gives the last variable the rest of the line
}

// repoAudit is `ccenv repo audit <org> [--quiet]`: unregistered folders in the workspace, and what's
// in quarantine.
func (a *App) repoAudit(ctx context.Context, o string, entries []repopolicy.Entry, quiet bool) error {
	registered := map[string]bool{}
	for _, e := range entries {
		registered[e.Dir] = true
	}
	ws := path.Join(a.Orgs.Dir, o, "workspace")
	var bad []string
	for _, n := range a.workspaceGlob(ws) {
		if n != ".claude" && !registered[n] {
			bad = append(bad, n)
		}
	}
	// qn=$(ls quarantine 2>/dev/null | wc -l): with no quarantine dir, ls fails, and pipefail + set -e
	// end the command here with exit 2, before anything below is printed (PARITY.md, legacy quirks).
	q := path.Join(a.Orgs.Dir, o, "quarantine")
	quarantined, err := a.lsNames(q)
	if err != nil {
		return &Exit{Code: 2}
	}
	if len(bad) > 0 {
		fmt.Fprintf(a.Stdout, "\nNot allowed in /workspace (policy: %s): %s\n", a.repoPolicyShown(o), strings.Join(bad, " "))
		fmt.Fprintf(a.Stdout, "  keep one -> %s repo adopt %s <dir>    (enforce mode quarantines them within 30s)\n", Tool, o)
	} else if !quiet {
		fmt.Fprintln(a.Stdout, "Workspace clean: only registered repos.")
	}
	if len(quarantined) > 0 {
		fmt.Fprintf(a.Stdout, "\nQuarantined (%s):\n", q)
		for _, n := range quarantined {
			fmt.Fprintln(a.Stdout, "  "+n)
		}
		fmt.Fprintf(a.Stdout, "  bring a repo back -> %s repo adopt %s <name>\n", Tool, o)
	}
	return nil
}

// repoPolicyShown is `$(envval "$org" REPO_POLICY | sed 's/^$/enforce/')`: an empty value shows as
// enforce, but a missing key shows as nothing (sed gets no line at all).
func (a *App) repoPolicyShown(o string) string {
	v, ok, _ := a.Orgs.Get(o, "REPO_POLICY")
	if ok && v == "" {
		return "enforce"
	}
	return v
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

// lsNames is `ls dir`: its visible entries, sorted; an error if dir can't be listed.
func (a *App) lsNames(dir string) ([]string, error) {
	entries, err := a.Host.FS.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
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
