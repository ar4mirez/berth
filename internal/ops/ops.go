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
	// Events is true when the operation reports its progress as events (`--output json` prints one
	// per line): the long ones, and the ones that stream.
	Events bool
	// NoJSON says why a reading operation returns neither. Every reading operation has JSON, Events
	// or NoJSON (TestReadsReturnDataOrSayWhyNot).
	NoJSON string
	// Subs, for Access BySub: the subcommands. "" is the default subcommand.
	Subs map[string]Op
}

var (
	readJSON = Op{Access: Read, JSON: true}
	readText = func(why string) Op { return Op{Access: Read, NoJSON: why} }
	write    = Op{Access: Write}
	// events marks a long operation: it reports its progress as events.
	events    = func(op Op) Op { op.Events = true; return op }
	writeMay  = func(note string) Op { return Op{Access: Write, Restart: Maybe, Note: note} }
	writeHard = func(note string) Op { return Op{Access: Write, Restart: Always, Note: note} }
)

// Catalog is every top-level command, by name.
var Catalog = map[string]Op{
	// Reading.
	"ls":           readJSON,
	"info":         readJSON,
	"whoami":       readJSON,
	"logs":         events(Op{Access: Read}), // a stream: one event per line
	"completion":   readText("a shell script"),
	"parity-check": readText("a line diff against ccenv, for the cutover"),
	"connect":      readText("holds an SSH tunnel open until interrupted"), // changes nothing (#58)

	// Lifecycle.
	"init":      write,
	"up":        events(writeHard("builds if needed, then recreates the container")),
	"restart":   events(writeHard("recreates the container")),
	"down":      writeHard("stops and removes the container"),
	"destroy":   writeHard("removes the container, the org's directory and (unless --keep-backups) its backups; irreversible, and needs a typed confirmation or --yes"),
	"build":     events(write), // builds berth's image; containers keep running on theirs until their next restart
	"pull":      events(write), // pulls the released image (#41); containers keep running on theirs
	"upgrade":   write,         // installs a verified release next to the current one and moves the link (#42); restarts nothing
	"image-tag": readJSON,      // hidden: the image tag this berth uses (for upgrade)
	"attach":    write,         // exec into the running container (acts as the org)
	"shell":     write,
	"claude":    write,
	"run":       write,
	"exec":      write,

	// Sign-in.
	"token":    writeMay("restarts a running org to apply the token, unless --no-restart"),
	"auth":     writeMay("the token step restarts a running org"),
	"login":    write, // restarts the Remote Control service inside the container, not the container
	"logout":   writeMay("--all on a running org restarts it to drop the token"),
	"gh-login": write,

	// Settings, per subcommand.
	"password": {Access: BySub, Subs: map[string]Op{
		"": readText("a secret: secrets never appear in JSON"), "show": readText("a secret: secrets never appear in JSON"),
		"rotate": writeMay("restarts a running org so the browser terminal uses the new password"),
	}},
	"env": {Access: BySub, Subs: map[string]Op{
		"": readJSON, "ls": readJSON, "list": readJSON,
		"set":   writeMay("restarts a running org unless --no-restart"),
		"unset": writeMay("restarts a running org unless --no-restart"),
		"rm":    writeMay("restarts a running org unless --no-restart"),
		// Takes secrets dropped inside the org (berth-secret-drop, #10), then as set.
		"accept": writeMay("restarts a running org unless --no-restart (accept --list changes nothing)"),
	}},
	"remote": {Access: BySub, Subs: map[string]Op{
		"": readJSON, "status": readJSON, "logs": events(Op{Access: Read}),
		"restart": write, // the Remote Control service, not the container
	}},
	"fw": {Access: BySub, Subs: map[string]Op{
		"": readJSON, "show": readJSON, "presets": readJSON, "test": readJSON,
		// Applied live inside a running container; no restart.
		"allow": write, "add": write, "deny": write, "remove": write, "rm": write,
		"on": write, "off": write, "edit": write, "reload": write,
	}},
	"repo": {Access: BySub, Subs: map[string]Op{
		"ls": readJSON, "list": readJSON, "audit": readJSON,
		"add": write, "new": write, "create": write, "publish": write, "rm": write, "remove": write,
		"adopt": write, "sync": write,
		// policy: showing reads; setting a mode restarts a running org to apply it.
		"policy": {Access: BySub, Subs: map[string]Op{"": readJSON, "<mode>": writeMay("restarts a running org to apply the policy")}},
	}},
	"clone": write,
	// System packages in an org's image (#106): the list is saved and the org's image built; the
	// container keeps running on its current image until its next restart.
	"pkg": {Access: BySub, Subs: map[string]Op{
		"": readJSON, "ls": readJSON, "list": readJSON, "presets": readJSON,
		"add": write, "rm": write, "remove": write, "build": write,
	}},

	// Backups.
	"backup":    events(write), // writes backup files; the org and its container are untouched
	"keygen":    write,
	"restore":   events(writeMay("starts the restored org unless --no-start; --force stops the org it replaces")),
	"rehydrate": write,
	"migrate":   write, // streams the org; nothing restarts here
	"schedule": {Access: BySub, Subs: map[string]Op{
		"status": readJSON,
		"":       write, "run": write, "now": write, "off": write, "--off": write,
	}},

	// Secrets as files (#37).
	"secrets": {Access: BySub, Subs: map[string]Op{
		"migrate": write, // moves values into files; the container keeps its environment until its next restart
	}},

	// Hosts (#44): the registry and berth's keys on the operator's machine, and berth's line in a
	// host's authorized_keys. No org or container on any host is touched.
	"host": {Access: BySub, Subs: map[string]Op{
		"ls": readJSON, "add": write, "rm": write, "rotate-access": write,
		// Cloud hosts (#52): a VM at the provider, and berth's registry. No org is touched.
		"create": write, "destroy": write,
		"reconcile": {Access: BySub, Subs: map[string]Op{"": readText("a report for a person"), "--prune": write}},
		// The host guard's rules apply live to running containers; nothing restarts.
		"guard": {Access: BySub, Subs: map[string]Op{"": readJSON, "status": readJSON, "on": write, "off": write}},
	}},

	// The default org (#55): showing it reads; setting or clearing it writes the operator's config.
	"use": {Access: BySub, Subs: map[string]Op{"": readJSON, "<org>": write, "--clear": write}},

	// berth serve as a service of the user (#63): a unit file and the service manager. No org is touched.
	"service": {Access: BySub, Subs: map[string]Op{
		"status": readText("a report for a person"), "logs": readText("the service's log, as text"),
		"install": write, "uninstall": write,
	}},

	// The dashboard (#59): it reads; each action in it is one of the operations here, checked as such.
	"tui": readText("an interactive dashboard: it needs a terminal"),

	// The API (#62): it serves the operations here, each checked as such for each caller.
	"serve": {Access: BySub, Subs: map[string]Op{
		"":          readText("the API server: clients talk to it over its socket"),
		"token ls":  readJSON,
		"token add": write, // the token store in the operator's ~/.config/berth; no org is touched
		"token rm":  write,
	}},

	// The MCP server (#61): read-only unless started with a flag that lets its tools write or restart.
	"mcp": {Access: BySub, Subs: map[string]Op{
		"":                 readText("an MCP server: it speaks the protocol on stdin and stdout"),
		"--allow-writes":   write,
		"--allow-restarts": writeMay("its lifecycle tools start, recreate or stop a container, each call confirmed with the org's name"),
	}},

	// Cutover and install.
	"takeover": write, // MANAGER=berth only; the container keeps running
	"handback": write,
	"install":  write,
}

// Groups (#55): each verb is a top-level command under another name (internal/cli/help.go).
var groups = map[string]map[string]string{
	"org": {"ls": "ls", "create": "init", "info": "info", "up": "up", "down": "down", "restart": "restart",
		"attach": "attach", "shell": "shell", "logs": "logs", "claude": "claude", "run": "run", "exec": "exec", "whoami": "whoami",
		"rehydrate": "rehydrate", "migrate": "migrate", "password": "password", "remote": "remote",
		"takeover": "takeover", "handback": "handback", "connect": "connect", "destroy": "destroy"},
	"account": {"signin": "auth", "token": "token", "login": "login", "logout": "logout", "gh": "gh-login", "whoami": "whoami"},
	"system":  {"install": "install", "upgrade": "upgrade", "pull": "pull", "build": "build", "completion": "completion", "parity-check": "parity-check", "service": "service"},
}

func init() {
	for g, verbs := range groups {
		op := Op{Access: BySub, Subs: map[string]Op{}}
		for verb, cmd := range verbs {
			op.Subs[verb] = Catalog[cmd]
		}
		Catalog[g] = op
	}
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
