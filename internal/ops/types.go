package ops

// Typed results for `--output json`. Each document carries a schema name with a version; a change
// that removes or renames a field bumps the version (docs/json.md).

// Orgs is `berth ls --output json`.
type Orgs struct {
	Schema string      `json:"schema"` // "berth.orgs/v1"
	Orgs   []OrgStatus `json:"orgs"`
	// Unreachable are the registered hosts whose orgs couldn't be listed (#45).
	Unreachable []HostError `json:"unreachable"`
	// Destroyed are the orgs `destroy` removed (offboarded) that don't exist again since.
	Destroyed []DestroyedOrg `json:"destroyed"`
	// MultiHost is true when hosts are registered: the text table then has a HOST column.
	MultiHost bool `json:"-"`
}

// DestroyedOrg is an org `destroy` removed, where, and when (RFC 3339, UTC).
type DestroyedOrg struct {
	Name string `json:"name"`
	Host string `json:"host"`
	At   string `json:"at"`
}

// HostError is a host that couldn't be reached, and why.
type HostError struct {
	Host  string `json:"host"`
	Error string `json:"error"`
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
	// Host is where the org is: "local" or a registered host's name (#45).
	Host string `json:"host"`
	// SSHRaw and TTYDRaw are the ports exactly as org.env has them: what the text table prints.
	SSHRaw  string `json:"-"`
	TTYDRaw string `json:"-"`
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
	// Engine is the host's container engine: docker or podman (#57).
	Engine string `json:"engine"`
	// Docker is the engine's version ("" when unknown).
	Docker string `json:"docker"`
	// Orgs is the number of orgs there (null when unknown).
	Orgs *int `json:"orgs"`
	// Error says what failed, if anything ("" otherwise).
	Error string `json:"error"`
}

// Info is `berth info <org> --output json`: every way to connect.
type Info struct {
	Schema    string `json:"schema"` // "berth.info/v1"
	Org       string `json:"org"`
	Container string `json:"container"`
	// State is "running" or "stopped".
	State string `json:"state"`
	// RemoteURL is the org's claude.ai/code environment ("" until Remote Control is set up, or
	// when the org is stopped).
	RemoteURL string `json:"remote_url"`
	// Address is where other devices reach the org's ports ("" if its bind can't be resolved).
	Address string `json:"address"`
	// LocalOnly is true when the ports are bound to this machine only.
	LocalOnly bool `json:"local_only"`
	// Ports are nil when org.env has no parseable value.
	SSHPort  *int `json:"ssh_port"`
	TTYDPort *int `json:"ttyd_port"`
	// User is the account in the container that SSH and the browser terminal use.
	User string `json:"user"`
	// SSHHost is the Host alias berth suggests for ~/.ssh/config.
	SSHHost string `json:"ssh_host"`
	// GitPublicKey is the org's git key ("" if it can't be read).
	GitPublicKey string `json:"git_public_key"`
	// Tunnel is set when the org is reached through an SSH tunnel (#58).
	Tunnel *Tunnel `json:"tunnel"`

	// What only the text needs: the address as ccenv words it, the ports as org.env has them, and
	// why the key couldn't be read.
	AddressText string `json:"-"`
	SSHRaw      string `json:"-"`
	TTYDRaw     string `json:"-"`
	KeyError    string `json:"-"`
}

// Tunnel is how to reach an org whose ports are bound to localhost.
type Tunnel struct {
	// Connect is the argument for `berth connect` ("acme", or "acme@box1").
	Connect string `json:"connect"`
	// SSHTarget is user@host for doing it by hand with `ssh -N -L` ("" when unknown).
	SSHTarget string `json:"ssh_target"`
}

// Whoami is `berth whoami [org...] --output json`: which accounts each org uses.
type Whoami struct {
	Schema string    `json:"schema"` // "berth.whoami/v1"
	Orgs   []Account `json:"orgs"`
}

// Account is one org in `whoami`.
type Account struct {
	Org     string `json:"org"`
	Running bool   `json:"running"`
	// Token is true when a Claude token is set (the value is never returned).
	Token bool `json:"token"`
	// GitHub is the gh CLI's login in the container ("" when not signed in, or the org is down).
	GitHub string `json:"github"`
	// Claude is the org's Remote Control login (nil when not logged in, or the org is down).
	Claude *ClaudeAccount `json:"claude"`

	// The two columns as ccenv words them.
	GitHubText string `json:"-"`
	ClaudeText string `json:"-"`
}

// ClaudeAccount is a Claude login.
type ClaudeAccount struct {
	Email          string `json:"email"`
	Organization   string `json:"organization"`
	OrganizationID string `json:"organization_id"`
}

// RemoteStatus is `berth remote <org> status --output json`: Remote Control in a running org.
type RemoteStatus struct {
	Schema string `json:"schema"` // "berth.remote/v1"
	Org    string `json:"org"`
	// State is "login-needed", "on", "blocked-by-org" or "restarting".
	State string `json:"state"`
	// URL is the org's claude.ai/code environment, when on ("" otherwise, or not yet logged).
	URL string `json:"url"`
	// Capacity is the service's last capacity line, when on ("" if it hasn't logged one).
	Capacity string `json:"capacity"`
}

// DefaultOrg is `berth use --output json`: the saved default org.
type DefaultOrg struct {
	Schema string `json:"schema"` // "berth.default-org/v1"
	// Org is "acme" or "acme@box1", or null when there is none.
	Org *string `json:"org"`
}

// Image is `berth image-tag --output json`.
type Image struct {
	Schema string `json:"schema"` // "berth.image/v1"
	Tag    string `json:"tag"`
}

// Repos is `berth repo ls <org> --output json`: the registered repos, and the audit `repo audit`
// gives.
type Repos struct {
	Schema string `json:"schema"` // "berth.repos/v1"
	Org    string `json:"org"`
	Repos  []Repo `json:"repos"`
	RepoAudit
}

// Repo is one registered repo.
type Repo struct {
	Dir string `json:"dir"`
	// Repo is the canonical form, host/path ("" for a local repo, or a URL with no canonical form).
	Repo string `json:"repo"`
	URL  string `json:"url"`
	// Local is true for a repo with no remote yet (`repo new --local`).
	Local bool `json:"local"`
	// Branch is the checked-out branch when the container can say, else the registered one ("" for
	// the remote's default).
	Branch string `json:"branch"`
	// State is "cloned", "missing", or "unknown" when the container is down and the folder is there.
	State string `json:"state"`
	// Changed is the number of uncommitted changes (null unless the running container reported it).
	Changed *int `json:"changed"`

	// The REPO, BRANCH and STATUS columns as ccenv words them.
	RepoText   string `json:"-"`
	BranchText string `json:"-"`
	StatusText string `json:"-"`
}

// RepoAudit is the workspace checked against the registry (`repo audit`).
type RepoAudit struct {
	// Policy is REPO_POLICY: "enforce", "warn" or "off" ("" when org.env has no such line).
	Policy string `json:"policy"`
	// Unregistered are the folders in /workspace that aren't registered repos.
	Unregistered []string `json:"unregistered"`
	// QuarantineDir is where enforce mode moves them, and Quarantined what is there now.
	QuarantineDir string   `json:"quarantine_dir"`
	Quarantined   []string `json:"quarantined"`
}

// RepoAuditDoc is `berth repo audit <org> --output json`.
type RepoAuditDoc struct {
	Schema string `json:"schema"` // "berth.repo-audit/v1"
	Org    string `json:"org"`
	RepoAudit
}

// RepoPolicy is `berth repo policy <org> --output json`.
type RepoPolicy struct {
	Schema string `json:"schema"` // "berth.repo-policy/v1"
	Org    string `json:"org"`
	// Policy is "enforce", "warn" or "off" ("" when org.env has no REPO_POLICY line).
	Policy string `json:"policy"`
}

// Presets is `berth fw <org> presets --output json`: the @presets the image knows.
type Presets struct {
	Schema  string   `json:"schema"` // "berth.firewall-presets/v1"
	Presets []Preset `json:"presets"`
	// Raw is the list as the image prints it.
	Raw string `json:"-"`
}

// Preset is one @preset and the hosts it allows.
type Preset struct {
	Name  string   `json:"name"` // "@python"
	Hosts []string `json:"hosts"`
}

// FirewallTest is `berth fw <org> test [host...] --output json`: what the org can reach now.
type FirewallTest struct {
	Schema  string          `json:"schema"` // "berth.firewall-test/v1"
	Org     string          `json:"org"`
	Results []FirewallProbe `json:"results"`
}

// FirewallProbe is one URL tried from inside the container.
type FirewallProbe struct {
	URL     string `json:"url"`
	Allowed bool   `json:"allowed"`
}

// Schedule is `berth schedule status --output json`: the nightly backup's schedule.
type Schedule struct {
	Schema string `json:"schema"` // "berth.schedule/v1"
	// Kind is "systemd" (a user timer), "cron" (a crontab line) or "none".
	Kind string `json:"kind"`
	// Timer is systemd's `list-timers` for the timer: its header line and the timer's.
	Timer []string `json:"timer"`
	// Runs are the last runs' lines from the service's journal (systemd).
	Runs []string `json:"runs"`
	// Jobs are the crontab's lines for the schedule, and Log where their output goes (cron).
	Jobs []string `json:"jobs"`
	Log  string   `json:"log"`

	// TimerText is `list-timers | head -2` as printed. NoRuns is true when the text says
	// "(no runs yet)": no matching journal line, or the journal couldn't be read.
	TimerText string `json:"-"`
	NoRuns    bool   `json:"-"`
}

// HostGuard is `berth host guard <name> status --output json`.
type HostGuard struct {
	Schema    string `json:"schema"` // "berth.host-guard/v1"
	Host      string `json:"host"`
	Installed bool   `json:"installed"`
	// State is the guard container's state ("running", …; "" when not installed).
	State string `json:"state"`
	// Rules is the guard's own report of its chains.
	Rules string `json:"rules"`
}

// Packages is `berth pkg <org> --output json`: the system packages an org's image adds (#106).
type Packages struct {
	Schema string `json:"schema"` // "berth.packages/v1"
	Org    string `json:"org"`
	File   string `json:"file"`
	// Entries are packages.txt's entries as written: package names and @presets.
	Entries []string `json:"entries"`
	// Packages are the packages they stand for: presets expanded, sorted.
	Packages []string `json:"packages"`
	// Invalid are entries that are neither a package name nor a known preset (a hand-edited file):
	// they are left out of the image.
	Invalid []string `json:"invalid"`
	// BaseImage is berth's image, and Image the one this org runs on: the same when it adds no
	// packages. Built is whether Image exists on the org's host.
	BaseImage string `json:"base_image"`
	Image     string `json:"image"`
	Built     bool   `json:"built"`
}

// PackagePresetsDoc is `berth pkg <org> presets --output json`.
type PackagePresetsDoc struct {
	Schema  string          `json:"schema"` // "berth.package-presets/v1"
	Presets []PackagePreset `json:"presets"`
}

// PackagePreset is one @preset and the packages it stands for.
type PackagePreset struct {
	Name     string   `json:"name"` // "@playwright-chromium"
	Packages []string `json:"packages"`
}
