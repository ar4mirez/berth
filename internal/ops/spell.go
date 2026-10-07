package ops

import (
	"regexp"
	"strings"
)

// BerthSpellings is whether messages name commands as berth's command line spells them (#140):
// `berth org create`, `berth env set acme KEY`. The CLI sets it; it is false for ccenv's command
// line (the ccenv alias, the parity suite), where they are `berth init`, `berth env acme set KEY`.
var BerthSpellings bool

// The messages in this code base are written with ccenv's spellings, which the parity suite
// compares with ccenv's own. Respell turns the commands one of them names into berth's.

// usages are whole usage lines, which no pattern below would get right.
var usages = strings.NewReplacer(
	"fw <org> [show|allow|deny|on|off|edit|reload|presets|test]", "fw show|allow|deny|on|off|edit|reload|presets|test <org>",
	"password <org> [show|rotate]", "org password show|rotate <org>",
	"env <org> [ls | set KEY | unset KEY] [--no-restart]", "env ls|set|unset|accept|migrate <org> … (see: berth env --help)",
	"remote <org> [status|logs|restart]", "account remote status|logs|restart <org>",
	"pkg <org> [ls | add <package|@preset>... | rm <entry>... | presets | build] [--no-build]", "pkg ls|add|rm|presets|build <org> … (see: berth pkg --help)",
	"schedule [--at HH:MM] [--keep N] [-o dir] | status | run | off", "backup schedule on [--at HH:MM] [--keep N] [-o dir] | status | run | off",
)

type respelling struct {
	re *regexp.Regexp
	to string
}

func rule(pattern, to string) respelling {
	return respelling{regexp.MustCompile(`\b` + Tool + ` ` + pattern), Tool + " " + to}
}

// An org, as a message writes it: a name, name@host, or a placeholder (<org>).
const anOrg = `(<org>|[a-z0-9][a-z0-9@-]*)`

var respellings = []respelling{
	// The org came before the verb.
	rule(`fw `+anOrg+` (show|allow|deny|on|off|edit|reload|presets|test)\b`, "fw $2 $1"),
	rule(`fw `+anOrg+` \.\.\.`, "fw allow|deny $1 ..."),
	rule(`env `+anOrg+` (ls|set|unset|accept)\b`, "env $2 $1"),
	rule(`pkg `+anOrg+` add\|rm`, "pkg add|rm $1"),
	rule(`pkg `+anOrg+` (ls|add|rm|presets|build)\b`, "pkg $2 $1"),
	rule(`pkg `+anOrg+`\)`, "pkg ls $1)"),
	rule(`password `+anOrg+` rotate\b`, "org password rotate $1"),
	rule(`password `+anOrg, "org password show $1"),
	rule(`remote `+anOrg+` (status|logs|restart)\b`, "account remote $2 $1"),
	// A command that moved into a group, or changed its name.
	rule(`init\b`, "org create"),
	rule(`run `+anOrg+` `, "org run $1 "),
	rule(`(destroy|exec|connect|rehydrate|migrate|takeover|handback|use)\b`, "org $1"),
	rule(`auth\b`, "account signin"),
	rule(`gh-login\b`, "account gh"),
	rule(`(token|login|logout|whoami)\b`, "account $1"),
	rule(`secrets migrate\b`, "env migrate"),
	rule(`keygen\b`, "backup keygen"),
	rule(`restore\b`, "backup restore"),
	rule(`backup (<org>|--all)`, "backup create $1"),
	rule(`schedule (status|run|off)\b`, "backup schedule $1"),
	rule(`schedule\b`, "backup schedule on"),
	rule(`(upgrade|pull|build)\b`, "system $1"),
}

// Respell is s with the commands it names spelled as this command line has them. With ccenv's
// spellings it is s.
func Respell(s string) string {
	if !BerthSpellings || !strings.Contains(s, Tool+" ") {
		return s
	}
	s = usages.Replace(s)
	for _, r := range respellings {
		s = r.re.ReplaceAllString(s, r.to)
	}
	return s
}
