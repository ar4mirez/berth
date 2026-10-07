package ops

import "testing"

// TestRespell: every kind of command a message names, in berth's spelling; and nothing else in the
// message changes.
func TestRespell(t *testing.T) {
	BerthSpellings = true
	t.Cleanup(func() { BerthSpellings = false })
	for in, want := range map[string]string{
		"unknown org 'acme' (run: berth init acme)":                                   "unknown org 'acme' (run: berth org create acme)",
		"(no custom variables; add one: berth env acme set KEY)":                      "(no custom variables; add one: berth env set acme KEY)",
		"usage: berth env acme@box1 unset KEY   (KEY like OPENROUTER_API_KEY)":        "usage: berth env unset acme@box1 KEY   (KEY like OPENROUTER_API_KEY)",
		"usage: berth env <org> [ls | set KEY | unset KEY] [--no-restart]":            "usage: berth env ls|set|unset|accept|migrate <org> … (see: berth env --help)",
		"usage: berth fw acme allow <domain|ip|cidr|@preset>...":                      "usage: berth fw allow acme <domain|ip|cidr|@preset>...",
		"usage: berth fw <org> [show|allow|deny|on|off|edit|reload|presets|test]":     "usage: berth fw show|allow|deny|on|off|edit|reload|presets|test <org>",
		"# Egress allowlist for acme, applied live by: berth fw acme ...":             "# Egress allowlist for acme, applied live by: berth fw allow|deny acme ...",
		"List presets: berth fw acme presets":                                         "List presets: berth fw presets acme",
		"(saved; applies on: berth up acme)":                                          "(saved; applies on: berth up acme)",
		"(no extra packages; add some: berth pkg acme add <package|@preset>)":         "(no extra packages; add some: berth pkg add acme <package|@preset>)",
		"managed with: berth pkg acme add|rm ...":                                     "managed with: berth pkg add|rm acme ...",
		"from its config/packages.txt (berth pkg acme). Don't edit":                   "from its config/packages.txt (berth pkg ls acme). Don't edit",
		"no password set (berth password acme rotate)":                                "no password set (berth org password rotate acme)",
		"(password: berth password acme@box1)":                                        "(password: berth org password show acme@box1)",
		"usage: berth password <org> [show|rotate]":                                   "usage: berth org password show|rotate <org>",
		"Remote Control: restarting (see: berth remote acme logs)":                    "Remote Control: restarting (see: berth account remote logs acme)",
		"usage: berth remote <org> [status|logs|restart]":                             "usage: berth account remote status|logs|restart <org>",
		"Mistake?   berth logout acme && berth auth acme. In the private window":      "Mistake?   berth account logout acme && berth account signin acme. In the private window",
		"Fix: berth gh-login acme (git then uses that login's token)":                 "Fix: berth account gh acme (git then uses that login's token)",
		"not logged in (berth login acme)":                                            "not logged in (berth account login acme)",
		"# ---- Claude account (use: berth token acme) ----":                          "# ---- Claude account (use: berth account token acme) ----",
		"(acme, the default org: berth use)":                                          "(acme, the default org: berth org use)",
		"usage: berth use [<org>[@host]] | --clear":                                   "usage: berth org use [<org>[@host]] | --clear",
		"usage: berth destroy <org> [--yes] [--keep-backups]":                         "usage: berth org destroy <org> [--yes] [--keep-backups]",
		"missing command; usage: berth exec <org> [--cwd DIR] -- <command>":           "missing command; usage: berth org exec <org> [--cwd DIR] -- <command>",
		"localhost with berth connect (docs/networking.md)":                           "localhost with berth org connect (docs/networking.md)",
		"To switch: berth migrate acme@box1 box2 --yes":                               "To switch: berth org migrate acme@box1 box2 --yes",
		"Undo: berth handback acme":                                                   "Undo: berth org handback acme",
		"use a key (berth keygen)":                                                    "use a key (berth backup keygen)",
		"before 'berth restore'.":                                                     "before 'berth backup restore'.",
		"usage: berth backup <org>...|--all [--plan]":                                 "usage: berth backup create <org>...|--all [--plan]",
		"(berth backup --all runs it by hand)":                                        "(berth backup create --all runs it by hand)",
		"Ran. See: berth schedule status":                                             "Ran. See: berth backup schedule status",
		"No backup schedule. Set one with: berth schedule":                            "No backup schedule. Set one with: berth backup schedule on",
		"usage: berth schedule [--at HH:MM] [--keep N] [-o dir] | status | run | off": "usage: berth backup schedule on [--at HH:MM] [--keep N] [-o dir] | status | run | off",
		"usage: berth secrets migrate <org> [--no-backup]":                            "usage: berth env migrate <org> [--no-backup]",
		"undo with: berth upgrade --rollback":                                         "undo with: berth system upgrade --rollback",
		"get it ready first with: berth pull (or berth build).":                       "get it ready first with: berth system pull (or berth system build).",
		`Headless                berth run acme "your prompt"`:                        `Headless                berth org run acme "your prompt"`,
		// Not commands.
		"berth isn't installed on box1, so it has no schedule":       "berth isn't installed on box1, so it has no schedule",
		"box1 runs darwin; berth schedules backups on Linux hosts":   "box1 runs darwin; berth schedules backups on Linux hosts",
		"a container started by an older berth can't be asked":       "a container started by an older berth can't be asked",
		"restart the server with: berth mcp --allow-writes":          "restart the server with: berth mcp --allow-writes",
		"berth repo add acme <owner/repo>   (only registered repos)": "berth repo add acme <owner/repo>   (only registered repos)",
		"berth restart acme recreates it":                            "berth restart acme recreates it",
	} {
		if got := Respell(in); got != want {
			t.Errorf("Respell(%q)\n got %q\nwant %q", in, got, want)
		}
	}
	BerthSpellings = false
	if s := "run: berth init acme"; Respell(s) != s {
		t.Error("ccenv's spellings were changed")
	}
}
