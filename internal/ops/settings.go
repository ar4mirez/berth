package ops

import (
	"context"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// GetEnv is `env <org> ls`: the org's custom variables, by name. A variable CCENV_ENV_KEYS lists
// with no line in org.env (and no file, #37) is not present.
func GetEnv(s System, org string) (Env, error) {
	out := Env{Schema: "berth.env/v1", Org: org, Vars: []EnvVar{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	data, _ := s.ReadFile(EnvPath(s, org))
	for _, k := range strings.Fields(env(s, org, contract.EnvKeys)) {
		present := s.SecretFile(org, k)
		for _, l := range lines(data) {
			present = present || strings.HasPrefix(l, k+"=")
		}
		out.Vars = append(out.Vars, EnvVar{Name: k, Present: present})
	}
	return out, nil
}

// FirewallTemplate is ccenv's write_firewall_template: a new org's firewall.txt. One line differs
// (PARITY.md): ccenv's says wildcards aren't supported, which stopped being true with #104.
func FirewallTemplate(org string) string {
	return Respell(`# Egress allowlist for ` + org + `, applied live by: ` + Tool + ` fw ` + org + ` ...
# One entry per line:  domain (pypi.org) | IP or CIDR (10.0.0.0/8) | @preset (@python) | mode on|off
# Always allowed: Anthropic/Claude, GitHub, npm.  List presets: ` + Tool + ` fw ` + org + ` presets
# A domain allows that name and every name under it; *.example.com means the same.
mode on
# Toolchain installs via mise, plus package registries for common languages:
@mise
@python
@go
@rust
@ruby
`)
}

// FirewallFile is an org's firewall.txt.
func FirewallFile(s System, org string) string {
	return path.Join(s.OrgsDir(), org, "config", "firewall.txt")
}

// GetFirewallEntries is the first half of `fw <org> show`: the allowlist's lines that aren't blank
// or comments, as written. A missing file reads as the template, which is what ccenv writes there
// before it looks.
func GetFirewallEntries(s System, org string) (Firewall, error) {
	out := Firewall{Schema: "berth.firewall/v1", Org: org, File: FirewallFile(s, org), Entries: []string{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	content, err := s.ReadFile(out.File)
	if err != nil {
		content = []byte(FirewallTemplate(org))
	}
	for _, l := range lines(content) {
		if t := strings.TrimLeft(l, " \t\n\v\f\r"); t != "" && !strings.HasPrefix(t, "#") {
			out.Entries = append(out.Entries, l)
		}
	}
	return out, nil
}

// FirewallLive is the second half: /run/firewall.status in the running container. live is nil
// when the org is down and "unknown" when the status can't be read. text is what ccenv prints
// after "live: ": whatever the read printed, then "unknown" if it failed.
func FirewallLive(ctx context.Context, s System, org string) (live *string, text string) {
	if !Running(ctx, s, org) {
		return nil, ""
	}
	// echo "live: $(docker exec … cat /run/firewall.status 2>/dev/null || echo unknown)"
	raw, err := s.CaptureRaw(ctx, true, "docker", "exec", contract.Container(org), "cat", contract.FirewallStatus)
	v := strings.TrimRight(raw, "\n")
	if err != nil {
		raw += "unknown\n"
		v = "unknown"
	}
	return &v, strings.TrimRight(raw, "\n")
}

// GetFirewall is `fw <org> show`: the allowlist and the live status.
func GetFirewall(ctx context.Context, s System, org string) (Firewall, error) {
	out, err := GetFirewallEntries(s, org)
	if err != nil {
		return out, err
	}
	out.Live, _ = FirewallLive(ctx, s, org)
	return out, nil
}

// GetHosts is `host ls`: this machine and every registered host.
func GetHosts(ctx context.Context, s System) (Hosts, error) {
	hs, err := s.Hosts(ctx)
	if hs == nil {
		hs = []HostStatus{}
	}
	return Hosts{Schema: "berth.hosts/v1", Hosts: hs}, err
}
