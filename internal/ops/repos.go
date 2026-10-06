package ops

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/repopolicy"
)

// ReposFile is an org's repos.txt.
func ReposFile(s System, org string) string {
	return path.Join(s.OrgsDir(), org, "config", "repos.txt")
}

// repoEntries is repo_entries: the registered repos (none when there is no file).
func repoEntries(s System, org string) []repopolicy.Entry {
	data, err := s.ReadFile(ReposFile(s, org))
	if err != nil {
		return nil
	}
	return repopolicy.Entries(data)
}

// GetRepos is `repo ls <org>`: each registered repo, with what the running container says of it,
// then the audit. The audit's failure (no quarantine dir yet, exit 2) comes back with the repos,
// which ccenv has printed by then.
func GetRepos(ctx context.Context, s System, org string) (Repos, error) {
	out := Repos{Schema: "berth.repos/v1", Org: org, Repos: []Repo{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	entries := repoEntries(s, org)
	for _, e := range entries {
		r := Repo{Dir: e.Dir, Repo: e.Canon, URL: e.URL, Branch: e.Branch, BranchText: e.Branch}
		switch {
		case Running(ctx, s, org) && s.Succeeds(ctx, false, "docker", "exec", contract.Container(org), "test", "-d", "/workspace/"+e.Dir+"/.git"):
			cur, n := repoDirty(ctx, s, org, e.Dir)
			r.State, r.StatusText, r.BranchText = "cloned", "cloned", cur
			if n != "0" {
				r.StatusText = "cloned, " + n + " changed"
			}
			if c, err := strconv.Atoi(n); err == nil {
				r.Changed = &c
			}
			if cur != "" && cur != "?" {
				r.Branch = cur
			}
		case s.IsDir(path.Join(s.OrgsDir(), org, "workspace", e.Dir, ".git")):
			r.State, r.StatusText = "unknown", "cloned (container down)"
		default:
			r.State, r.StatusText = "missing", "MISSING ("+Tool+" repo sync "+org+")"
		}
		if r.Branch == "-" {
			r.Branch = ""
		}
		if r.BranchText == "-" || r.BranchText == "" {
			r.BranchText = "default"
		}
		if r.RepoText = e.Canon; r.RepoText == "" {
			r.RepoText = "-" // repo_entries' placeholder for a URL with no canonical form
		}
		if e.URL == "local" {
			r.Local, r.Repo, r.RepoText = true, "", "(local only, not published)"
		}
		out.Repos = append(out.Repos, r)
	}
	var err error
	out.RepoAudit, err = audit(s, org, entries)
	return out, err
}

// repoDirty is
//
//	read -r cur n < <(docker exec -u node -w /workspace/<dir> … sh -c '…' 2>/dev/null || echo "? ?")
//
// the current branch and the number of changed files, as the first line's fields.
func repoDirty(ctx context.Context, s System, org, dir string) (string, string) {
	out, err := s.CaptureRaw(ctx, true, "docker", "exec", "-u", "node", "-w", "/workspace/"+dir, contract.Container(org), "sh", "-c",
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

// GetRepoAudit is `repo audit <org>`: unregistered folders in the workspace, and what's in
// quarantine. With no quarantine dir (an org that never started), ccenv's `ls` fails and the
// command ends with exit 2 and nothing printed (PARITY.md, legacy quirks).
func GetRepoAudit(s System, org string) (RepoAuditDoc, error) {
	out := RepoAuditDoc{Schema: "berth.repo-audit/v1", Org: org}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	var err error
	out.RepoAudit, err = audit(s, org, repoEntries(s, org))
	return out, err
}

func audit(s System, org string, entries []repopolicy.Entry) (RepoAudit, error) {
	a := RepoAudit{Policy: RepoPolicyShown(s, org), Unregistered: []string{}, Quarantined: []string{},
		QuarantineDir: path.Join(s.OrgsDir(), org, "quarantine")}
	registered := map[string]bool{}
	for _, e := range entries {
		registered[e.Dir] = true
	}
	for _, n := range workspaceGlob(s, path.Join(s.OrgsDir(), org, "workspace")) {
		if n != ".claude" && !registered[n] {
			a.Unregistered = append(a.Unregistered, n)
		}
	}
	// qn=$(ls quarantine 2>/dev/null | wc -l): with no quarantine dir, ls fails, and pipefail + set -e
	// end the command here with exit 2, before anything below is printed.
	names, err := s.DirNames(a.QuarantineDir)
	if err != nil {
		return a, &Exit{Code: 2}
	}
	for _, n := range names { // ls: no hidden entries, sorted
		if !strings.HasPrefix(n, ".") {
			a.Quarantined = append(a.Quarantined, n)
		}
	}
	sort.Strings(a.Quarantined)
	return a, nil
}

// workspaceGlob is `for e in ws/* ws/.[!.]*`: the visible entries, then the hidden ones (not . or
// .., nor names starting with ".."), each sorted; a dangling symlink is skipped ([ -e ]).
func workspaceGlob(s System, ws string) []string {
	names, err := s.DirNames(ws)
	if err != nil {
		return nil
	}
	var visible, hidden []string
	for _, n := range names {
		if !s.Exists(path.Join(ws, n)) {
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

// RepoPolicyShown is REPO_POLICY as ccenv shows it: a line with no value reads as "enforce", and
// no line at all as "".
func RepoPolicyShown(s System, org string) string {
	v, ok := s.EnvGet(org, "REPO_POLICY")
	if ok && v == "" {
		return "enforce"
	}
	return v
}

// GetRepoPolicy is `repo policy <org>` with no mode.
func GetRepoPolicy(s System, org string) (RepoPolicy, error) {
	out := RepoPolicy{Schema: "berth.repo-policy/v1", Org: org}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	out.Policy = RepoPolicyShown(s, org)
	return out, nil
}
