// Package ops is berth's catalog of operations: for every command (and, where it depends, every
// subcommand) whether it writes and whether it restarts a container, plus the typed results of the
// operations that return data. The CLI, and later the TUI, MCP and the API (#54, #59, #61, #62),
// read it to confirm or refuse restarts and writes, so "nothing restarts by surprise" holds in
// every interface. TestCatalogCoversEveryCommand keeps it complete.
package ops

import "strings"

// Access is whether an operation changes anything.
type Access int

const (
	// Read changes nothing: allowed under --read-only.
	Read Access = iota + 1
	// Write changes org state, host files, or containers: refused under --read-only.
	Write
	// BySub depends on the subcommand; Subs says which.
	BySub
)

// Restart is whether an operation recreates or stops an org's container, stopping the work
// running in it.
type Restart int

const (
	// Never: no container is recreated or stopped.
	Never Restart = iota
	// Maybe: recreates a running org's container unless told not to (--no-restart, --no-start), or
	// only in some cases (see Note).
	Maybe
	// Always: recreates or stops the container every time.
	Always
)

// Op describes one operation.
type Op struct {
	Access  Access
	Restart Restart
	// Note says when a Maybe restart happens, or anything else a caller must know.
	Note string
	// JSON is true when the operation returns data (`--output json`).
	JSON bool
	// Subs, for Access BySub: the subcommands. "" is the default subcommand.
	Subs map[string]Op
}

var (
	read      = Op{Access: Read}
	readJSON  = Op{Access: Read, JSON: true}
	write     = Op{Access: Write}
	writeMay  = func(note string) Op { return Op{Access: Write, Restart: Maybe, Note: note} }
	writeHard = func(note string) Op { return Op{Access: Write, Restart: Always, Note: note} }
)

// Catalog is every top-level command, by name.
var Catalog = map[string]Op{
	// Reading.
	"ls":           readJSON,
	"info":         read,
	"whoami":       read,
	"logs":         read,
	"completion":   read,
	"parity-check": read,

	// Lifecycle.
	"init":    write,
	"up":      writeHard("builds if needed, then recreates the container"),
	"restart": writeHard("recreates the container"),
	"down":    writeHard("stops and removes the container"),
	"build":   write, // builds berth's image; containers keep running on theirs until their next restart
	"pull":    write, // pulls the released image (#41); containers keep running on theirs
	"attach":  write, // exec into the running container (acts as the org)
	"shell":   write,
	"claude":  write,
	"run":     write,

	// Sign-in.
	"token":    writeMay("restarts a running org to apply the token, unless --no-restart"),
	"auth":     writeMay("the token step restarts a running org"),
	"login":    write, // restarts the Remote Control service inside the container, not the container
	"logout":   writeMay("--all on a running org restarts it to drop the token"),
	"gh-login": write,

	// Settings, per subcommand.
	"password": {Access: BySub, Subs: map[string]Op{
		"": read, "show": read,
		"rotate": writeMay("restarts a running org so the browser terminal uses the new password"),
	}},
	"env": {Access: BySub, Subs: map[string]Op{
		"": readJSON, "ls": readJSON, "list": readJSON,
		"set":   writeMay("restarts a running org unless --no-restart"),
		"unset": writeMay("restarts a running org unless --no-restart"),
		"rm":    writeMay("restarts a running org unless --no-restart"),
	}},
	"remote": {Access: BySub, Subs: map[string]Op{
		"": read, "status": read, "logs": read,
		"restart": write, // the Remote Control service, not the container
	}},
	"fw": {Access: BySub, Subs: map[string]Op{
		"": readJSON, "show": readJSON, "presets": read, "test": read,
		// Applied live inside a running container; no restart.
		"allow": write, "add": write, "deny": write, "remove": write, "rm": write,
		"on": write, "off": write, "edit": write, "reload": write,
	}},
	"repo": {Access: BySub, Subs: map[string]Op{
		"ls": read, "list": read, "audit": read,
		"add": write, "new": write, "create": write, "publish": write, "rm": write, "remove": write,
		"adopt": write, "sync": write,
		// policy: showing reads; setting a mode restarts a running org to apply it.
		"policy": {Access: BySub, Subs: map[string]Op{"": read, "<mode>": writeMay("restarts a running org to apply the policy")}},
	}},
	"clone": write,

	// Backups.
	"backup":    write, // writes backup files; the org and its container are untouched
	"keygen":    write,
	"restore":   writeMay("starts the restored org unless --no-start; --force stops the org it replaces"),
	"rehydrate": write,
	"migrate":   write, // streams the org; nothing restarts here
	"schedule": {Access: BySub, Subs: map[string]Op{
		"status": read,
		"":       write, "run": write, "now": write, "off": write, "--off": write,
	}},

	// Secrets as files (#37).
	"secrets": {Access: BySub, Subs: map[string]Op{
		"migrate": write, // moves values into files; the container keeps its environment until its next restart
	}},

	// Cutover and install.
	"takeover": write, // MANAGER=berth only; the container keeps running
	"handback": write,
	"install":  write,
}

// Lookup returns the operation for a command and its subcommand ("" for none). A nested
// subcommand is a path: "policy/<mode>" for `repo policy <org> <mode>`, "policy/" for showing it.
// ok is false for an unknown command or subcommand.
func Lookup(cmd, sub string) (Op, bool) {
	op, ok := Catalog[cmd]
	if !ok {
		return op, false
	}
	for _, part := range strings.Split(sub, "/") {
		if op.Access != BySub {
			break
		}
		if op, ok = op.Subs[part]; !ok {
			return op, false
		}
	}
	return op, true
}
