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
	// later: plain arguments, and no endpoint yet.
	later = "no endpoint yet: plain arguments, coming with the rest of #167's phase 2"
	// secret: it takes or shows a secret.
	secret = "it takes or shows a secret: #167's phase 3"
	// terminal: it needs a terminal.
	terminal = "it needs a terminal: #167's phase 4"
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
	"host reconcile/": alias + "host ls, as a report for a person",

	// Plain arguments, not yet.
	"env unset": later, "pkg build": later, "repo adopt": later, "repo audit": later, "repo new": later, "repo publish": later,
	"repo policy/<mode>": later, "schedule ": later, "schedule run": later, "schedule off": later,
	"host add": later, "host rm": later, "host rotate-access": later, "host guard/on": later, "host guard/off": later,
	"host guard/status": later, "host reconcile/--prune": later,
	"restore ": later, "rehydrate ": later, "migrate ": later, "pull ": later, "build ": later, "upgrade ": later,
	"logout ": later, "secrets migrate": later, "remote logs": later, "run ": later, "serve token ls": later,
	"use ": later, "use <org>": later, "use --clear": later,

	// Secrets.
	"env set": secret, "env accept": secret, "token ": secret, "password show": secret, "password rotate": secret,
	"keygen ": secret, "serve token add": secret, "serve token rm": secret, "host create": secret, "host destroy": secret,

	// A terminal.
	"auth ": terminal, "login ": terminal, "gh-login ": terminal, "attach ": terminal, "shell ": terminal, "claude ": terminal,
	"exec ": terminal, "fw edit": terminal,

	// Not for the API.
	"completion ": never + "a shell script", "connect ": never + "a tunnel from the operator's own machine",
	"install ": never + "it installs berth on a machine", "parity-check ": never + "the cutover from ccenv",
	"takeover ": never + "the cutover from ccenv", "handback ": never + "the cutover from ccenv",
	"image-tag ": never + "for berth's own upgrade",
	"mcp ":       never + "a server", "mcp --allow-writes": never + "a server", "mcp --allow-restarts": never + "a server",
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
