// Package contract is what the host tool and the container image must agree on: where things are
// mounted, the files the container reads from /config, what it writes for the host to read, the
// tmux session, and the env vars it takes from org.env. berth's commands use these constants, and
// contract_test.go checks each against image/ and compose.yml as shipped, so a change on one side
// fails until the other matches. These are wire contracts: they keep ccenv's names (plan).
package contract

// Container is the name of an org's container (compose.yml: container_name: claude-${ORG}).
func Container(org string) string { return "claude-" + org }

// Mounts in the container (compose.yml volumes).
const (
	Workspace = "/workspace"         // ${ORG_DIR}/workspace
	ClaudeDir = "/home/node/.claude" // ${ORG_DIR}/claude, and CLAUDE_CONFIG_DIR (Dockerfile)
	Config    = "/config"            // ${ORG_DIR}/config, read-only
)

// Files the container reads from /config (the host writes them under <org>/config/).
const (
	ReposFile      = Config + "/repos.txt"               // repo-policy.sh, repo-guard.js, the git guard
	FirewallFile   = Config + "/firewall.txt"            // init-firewall.sh
	AuthorizedKeys = Config + "/authorized_keys"         // entrypoint.sh, installed for sshd
	TTYDCredential = Config + "/secrets/ttyd_credential" // entrypoint.sh, ttyd -c
	// SecretsEnv holds one file per secret or custom variable (#37): secrets-env.sh exports them at
	// start, and berth's exec loader for `claude`/`run` reads them.
	SecretsEnv = Config + "/secrets/env"
	// SetupScript is a profile's setup (berth init --profile): entrypoint.sh runs it as node at start.
	SetupScript = Config + "/setup.sh"
)

// What the container writes for the host to read.
const (
	FirewallStatus   = "/run/firewall.status"            // init-firewall.sh; `fw show`'s live line
	RemoteControlLog = ClaudeDir + "/remote-control.log" // entrypoint.sh (RC_LOG); `remote`, `ls`, `login`
	Credentials      = ClaudeDir + "/.credentials.json"  // Claude Code's full login ($CLAUDE_CONFIG_DIR)
	SetupLog         = "/run/berth-setup.log"            // entrypoint.sh: the setup script's output
	FirewallScript   = "init-firewall.sh"                // on PATH: `apply` (the default) and `presets`
	SecretDrop       = "berth-secret-drop"               // on PATH: --list and --show KEY, for `env accept` (#10)
	RemoteRetry      = "/run/rc-retry"                   // rc-supervisor.sh: created to end its wait now (#7)
	GitTransport     = "git-transport"                   // on PATH: `apply` (the default) and `show` (#103)
	TmuxSession      = "main"                            // entrypoint.sh; attach and ttyd join it
	EnvKeys          = "CCENV_ENV_KEYS"                  // org.env: custom vars the entrypoint snapshots for SSH
	SSHPortInside    = "2222"                            // compose.yml: ${SSH_PORT}:2222
	TTYDPortInside   = "7681"                            // compose.yml: ${TTYD_PORT}:7681
)

// Lines Claude Code itself writes to the Remote Control log, which the host greps for. They come
// from Claude Code, not from image/, so contract_test.go can't check them; the parity scenarios pin
// how they are matched.
const (
	RemoteControlBlocked  = "blocked by organization policy"
	RemoteControlCapacity = "Capacity"
	RemoteControlStarting = "starting remote-control" // rc-supervisor.sh logs it before each attempt
)
