package host

import (
	"fmt"
)

// OrgSpec is the container an org runs, as compose.yml describes it (#43, phase 1 of
// docs/prd/043-engine-api.md): one description, from what berth gives compose and the org's
// org.env. Apple's container starts orgs from it, and the integration suite checks it against
// `docker compose config` for the same org, so the two can't drift apart. berth still starts orgs
// on Docker and Podman with compose (CLAUDE.md).
type OrgSpec struct {
	// Project is compose's project (claude-<org>), Name the container's, Hostname its hostname.
	Project, Name, Hostname string
	// Image is the image it runs; BuildContext and BuildArgs build it when it isn't there.
	Image        string
	BuildContext string
	BuildArgs    map[string]string
	// EnvFile is the org's org.env, every line of which is in the container's environment, and
	// Env what compose.yml's environment: adds (ORG, HOST_UID, HOST_GID).
	EnvFile string
	Env     map[string]string
	Ports   []PortSpec
	Mounts  []MountSpec
	CapAdd  []string
	// Memory and CPUs are the limits as written (8g, 4 or 0.5).
	Memory, CPUs string
	// Restart is the restart policy; Init whether the engine adds its own init (tini is the
	// entrypoint, so no).
	Restart string
	Init    bool
	// UsernsMode and User are set for rootless Podman (keep-id, 0:0), and empty otherwise.
	UsernsMode, User string
}

// PortSpec publishes Container (in the container) on HostIP:Host.
type PortSpec struct{ HostIP, Host, Container string }

// MountSpec binds Source (on the host) at Target.
type MountSpec struct {
	Source, Target string
	ReadOnly       bool
}

// orgMounts are compose.yml's binds: the org directory's subdirectory, where it goes, read-only.
var orgMounts = []struct {
	dir, target string
	ro          bool
}{
	{"workspace", "/workspace", false}, {"claude", "/home/node/.claude", false}, {"mise", "/home/node/.mise", false},
	{"home-config", "/home/node/.config", false}, {"ssh", "/opt/claude-secrets/ssh", true}, {"sshd", "/etc/ssh/keys", false},
	{"config", "/config", true}, {"quarantine", "/quarantine", false},
}

// NewOrgSpec is compose.yml for one org. env is what berth gives compose (ORG, ORG_DIR,
// BIND_ADDR, HOST_UID, HOST_GID, the image's variables, BERTH_USERNS and BERTH_USER, CPUS), org is
// the org's org.env, and envFile its path. A variable is looked up as compose does: the
// process's environment first, then the env file.
func NewOrgSpec(env, org map[string]string, envFile string) (OrgSpec, error) {
	get := func(k, def string) string {
		if v := env[k]; v != "" {
			return v
		}
		if v := org[k]; v != "" {
			return v
		}
		return def
	}
	name, dir := env["ORG"], env["ORG_DIR"]
	if name == "" || dir == "" {
		return OrgSpec{}, fmt.Errorf("an org's container needs ORG and ORG_DIR")
	}
	ssh, ttyd := get("SSH_PORT", ""), get("TTYD_PORT", "")
	if ssh == "" || ttyd == "" {
		return OrgSpec{}, fmt.Errorf("%s has no SSH_PORT or TTYD_PORT", envFile)
	}
	bind := get("BIND_ADDR", "127.0.0.1")
	s := OrgSpec{
		Project: "claude-" + name, Name: "claude-" + name, Hostname: name,
		Image:        get("CLAUDE_ENV_IMAGE", "claude-env") + ":" + get("IMAGE_TAG", "latest"),
		BuildContext: get("CLAUDE_ENV_IMAGE_DIR", "./image"),
		BuildArgs: map[string]string{
			"USER_UID": get("HOST_UID", "1000"), "USER_GID": get("HOST_GID", "1000"), "CLAUDE_CODE_VERSION": get("CLAUDE_CODE_VERSION", ""),
		},
		EnvFile: envFile,
		Env:     map[string]string{"ORG": name, "HOST_UID": get("HOST_UID", ""), "HOST_GID": get("HOST_GID", "")},
		Ports:   []PortSpec{{bind, ssh, "2222"}, {bind, ttyd, "7681"}},
		CapAdd:  []string{"NET_ADMIN", "NET_RAW"},
		Memory:  get("MEM_LIMIT", "8g"), CPUs: get("CPUS", "4"),
		Restart:    "unless-stopped",
		UsernsMode: get("BERTH_USERNS", ""), User: get("BERTH_USER", ""),
	}
	for _, m := range orgMounts {
		s.Mounts = append(s.Mounts, MountSpec{Source: dir + "/" + m.dir, Target: m.target, ReadOnly: m.ro})
	}
	return s, nil
}
