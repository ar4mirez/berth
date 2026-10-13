package api

import (
	"sort"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/ops"
)

// Why an operation of the catalog has no endpoint (#167). "Every action in both dashboards" is
// kept by this table: an operation is served, or it is here with its reason, and a new command
// fails the test until it is one or the other.
const (
	// alias: another spelling of an operation that is served, or that is listed under its own name.
	alias = "another spelling of "
	// shown: it gives a secret back, which the API never does.
	shown = "it shows a secret, and the API never returns one: it is read on the machine itself"
	// cloud: it needs a cloud provider's key.
	cloud = "it needs a cloud provider's key in berth's own environment, which a server shouldn't hold: the command line"
	// signin: a sign-in, which needs a terminal: the API runs it on one of its own.
	signin = "served on a terminal of its own, not as a tool: POST /v1/terminal"
	// session: a session in the org, which the org has its own ways into.
	session = "a session in the org: the org's browser terminal and SSH are the ways in, and the dashboards link to them"
	// never: it isn't something a dashboard or an API caller does.
	never = "not for the API: "
)

var notServed = map[string]string{
	// Other spellings.
	"clone ": alias + "repo add", "env ": alias + "env ls", "env list": alias + "env ls", "env rm": alias + "env unset",
	"fw ": alias + "fw show", "fw add": alias + "fw allow", "fw remove": alias + "fw deny", "fw rm": alias + "fw deny",
	"pkg ": alias + "pkg ls", "pkg list": alias + "pkg ls", "pkg remove": alias + "pkg rm",
	"repo list": alias + "repo ls", "repo remove": alias + "repo rm", "repo create": alias + "repo new",
	"repo policy/": alias + "repo ls, which has the policy", "remote ": alias + "remote status", "password ": alias + "password show",
	"schedule --off": alias + "schedule off", "schedule now": alias + "schedule run", "host guard/": alias + "host guard/status",
	"host reconcile/": cloud,

	// Secrets.
	"password show": shown,
	"host create":   cloud, "host destroy": cloud, "host reconcile/--prune": cloud,

	// A terminal.
	"auth ": signin, "login ": signin, "gh-login ": signin,
	"attach ": session, "shell ": session, "claude ": session, "exec ": session,
	"fw edit": never + "an editor on the allowlist: allow and deny are",

	// Not for the API.
	"completion ": never + "a shell script", "connect ": never + "a tunnel from the operator's own machine",
	"install ": never + "it installs berth on a machine", "parity-check ": never + "the cutover from ccenv",
	"takeover ": never + "the cutover from ccenv", "handback ": never + "the cutover from ccenv",
	"image-tag ": never + "for berth's own upgrade",
	"use ":       never + "the command line's default org: an API caller names the org", "use <org>": never + "the command line's default org: an API caller names the org",
	"use --clear": never + "the command line's default org: an API caller names the org",
	"mcp ":        never + "a server", "mcp --allow-writes": never + "a server", "mcp --allow-restarts": never + "a server",
	"serve ": never + "the server itself", "tui ": never + "a dashboard", "ui ": never + "a dashboard",
	"service install": never + "it manages the server that would be asked", "service uninstall": never + "it manages the server that would be asked",
	"service status": never + "it manages the server that would be asked", "service logs": never + "it manages the server that would be asked",
}

// leaves are the catalog's operations, as "cmd sub": every command, and every subcommand of one
// whose access depends on it. The noun groups (org, account, system) repeat commands that have
// their own names.
func leaves() []string {
	var out []string
	var walk func(name, sep string, op ops.Op)
	walk = func(name, sep string, op ops.Op) {
		if op.Access != ops.BySub {
			if sep == " " {
				name += " " // a command with no subcommand: "up "
			}
			out = append(out, name)
			return
		}
		for sub, o := range op.Subs {
			walk(name+sep+sub, "/", o) // "fw allow", then "repo policy/<mode>"
		}
	}
	for name, op := range ops.Catalog {
		if name == "org" || name == "account" || name == "system" {
			continue
		}
		walk(name, " ", op)
	}
	sort.Strings(out)
	return out
}

// TestEveryOperationIsServedOrSaysWhyNot: the API has an endpoint for each of the catalog's
// operations, or this file says why it doesn't.
func TestEveryOperationIsServedOrSaysWhyNot(t *testing.T) {
	served := map[string]string{}
	for _, s := range Specs() {
		if s.Cmd != "" {
			served[s.Cmd+" "+s.Sub] = s.Name
		}
	}
	routed := map[string]bool{}
	for _, r := range Routes {
		routed[r.Tool] = true
	}
	all := map[string]bool{}
	for _, l := range leaves() {
		all[l] = true
	}
	for _, l := range leaves() {
		all[l] = true
		tool, ok := served[l]
		why, listed := notServed[l]
		switch {
		case ok && listed:
			t.Errorf("%q is served by %s, and still listed as not served (%s)", l, tool, why)
		case ok && !routed[tool]:
			t.Errorf("%q is the tool %s, which has no route", l, tool)
		case !ok && !listed:
			t.Errorf("%q has no endpoint and no reason: add a tool and a route, or say why not in notServed", l)
		case listed && strings.HasPrefix(why, alias):
			// What it is another spelling of is an operation, served or listed.
			of, _, _ := strings.Cut(strings.TrimPrefix(why, alias), ",")
			if !all[of] && !all[of+" "] {
				t.Errorf("%q is listed as another spelling of %q, which isn't an operation", l, of)
			}
		}
	}
	for l := range notServed {
		if !all[l] {
			t.Errorf("notServed lists %q, which isn't an operation of the catalog", l)
		}
	}
	for l, tool := range served {
		if !all[l] {
			t.Errorf("the tool %s is for %q, which isn't an operation of the catalog", tool, l)
		}
	}
}

// TestAdminOperations: what can't be undone, or handles a secret, is no MCP server's to offer, and
// the OpenAPI document says what it needs.
func TestAdminOperations(t *testing.T) {
	n := 0
	for _, s := range Specs() {
		if !s.Op.Admin {
			continue
		}
		n++
		if !s.NoMCP {
			t.Errorf("%s is an admin operation, and an MCP server would offer it", s.Name)
		}
		if access(s.Op) != "admin" {
			t.Errorf("%s: the document says it needs %q", s.Name, access(s.Op))
		}
	}
	if n == 0 {
		t.Error("no tool is an admin operation: org_destroy should be")
	}
}

// TestTerminalRunsTheSignIns: each operation listed as served on a terminal is one a terminal
// runs, and a terminal runs nothing else: berth's own sign-in commands, each a write in the catalog.
func TestTerminalRunsTheSignIns(t *testing.T) {
	onTerminal := map[string]bool{}
	for name, c := range TerminalCommands {
		if len(c.Args) != 2 || c.Args[0] != "account" || c.About == "" {
			t.Errorf("%s runs berth %v: not one of the account group's commands, or it says nothing of itself", name, c.Args)
			continue
		}
		op, ok := ops.Lookup(c.Args[0], c.Args[1])
		if !ok || op.Access != ops.Write {
			t.Errorf("%s runs berth %v, which isn't a write in the catalog", name, c.Args)
		}
		onTerminal[name] = true
	}
	// The flat names the catalog has them under, and the name a caller asks with.
	for flat, name := range map[string]string{"auth ": "signin", "login ": "login", "gh-login ": "gh"} {
		if notServed[flat] != signin {
			t.Errorf("%q isn't listed as served on a terminal", flat)
		}
		if !onTerminal[name] {
			t.Errorf("%q is listed as served on a terminal, and no terminal runs %s", flat, name)
		}
		delete(onTerminal, name)
	}
	for name := range onTerminal {
		t.Errorf("a terminal runs %s, which the table doesn't list", name)
	}
}
