package ops

// Typed results for `--output json`. Each document carries a schema name with a version; a change
// that removes or renames a field bumps the version (docs/json.md).

// Orgs is `berth ls --output json`.
type Orgs struct {
	Schema string      `json:"schema"` // "berth.orgs/v1"
	Orgs   []OrgStatus `json:"orgs"`
}

// OrgStatus is one org in `ls`.
type OrgStatus struct {
	Name string `json:"name"`
	// Manager is "berth" or "ccenv" (no MANAGER line means ccenv's).
	Manager string `json:"manager"`
	// State is "up" or "down".
	State string `json:"state"`
	// Ports are nil when org.env has no parseable value.
	SSHPort  *int `json:"ssh_port"`
	TTYDPort *int `json:"ttyd_port"`
	// Token is true when a Claude token or API key is set (the value is never returned).
	Token bool `json:"token"`
	// Remote is Remote Control's state: "-" (down), "off", "login-needed", "on", "blocked-by-org",
	// or "restarting".
	Remote string `json:"remote"`
}

// Env is `berth env <org> ls --output json`: the custom variables' names, never their values.
type Env struct {
	Schema string   `json:"schema"` // "berth.env/v1"
	Org    string   `json:"org"`
	Vars   []EnvVar `json:"vars"`
}

// EnvVar is one custom variable.
type EnvVar struct {
	Name string `json:"name"`
	// Present is false when CCENV_ENV_KEYS lists it but org.env has no line for it.
	Present bool `json:"present"`
}

// Firewall is `berth fw <org> show --output json`.
type Firewall struct {
	Schema string `json:"schema"` // "berth.firewall/v1"
	Org    string `json:"org"`
	File   string `json:"file"`
	// Entries are firewall.txt's lines that aren't blank or comments, as written.
	Entries []string `json:"entries"`
	// Live is /run/firewall.status in the running container ("on 159", "off"), "unknown" if it
	// can't be read, or nil when the org is down.
	Live *string `json:"live"`
}

// Hosts is `berth host ls --output json` (#44).
type Hosts struct {
	Schema string       `json:"schema"` // "berth.hosts/v1"
	Hosts  []HostStatus `json:"hosts"`
}

// HostStatus is one host in `host ls`. The first is always this machine, "local".
type HostStatus struct {
	Name string `json:"name"`
	// Kind is "local" or "ssh".
	Kind string `json:"kind"`
	// Address is user@host:port ("" for local).
	Address string `json:"address"`
	// Home is the state root on that host.
	Home      string `json:"home"`
	Reachable bool   `json:"reachable"`
	// Docker is the engine's version ("" when unknown).
	Docker string `json:"docker"`
	// Orgs is the number of orgs there (null when unknown).
	Orgs *int `json:"orgs"`
	// Error says what failed, if anything ("" otherwise).
	Error string `json:"error"`
}
