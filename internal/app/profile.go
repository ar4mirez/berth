package app

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/ar4mirez/berth/internal/contract"
	"github.com/ar4mirez/berth/internal/ops"
)

// ProfileSchema is the version a profile file names in `profile:`.
const ProfileSchema = "berth.profile/v1"

// Profile is what `init <org> --profile <file>` applies to the new org (docs/guides/profiles.md).
type Profile struct {
	Schema   string            `yaml:"profile"`
	Provider string            `yaml:"provider"` // anthropic (default) | bedrock | vertex | openrouter
	Region   string            `yaml:"region"`   // bedrock and vertex
	Env      map[string]string `yaml:"env"`      // set as `env set` does, listed in CCENV_ENV_KEYS
	Secrets  []string          `yaml:"secrets"`  // variables the operator sets after init: printed as next steps
	Firewall []string          `yaml:"firewall"` // firewall.txt entries, in place of the template's presets
	Setup    string            `yaml:"setup"`    // bash, run as node at every container start (config/setup.sh)
}

// providers are the model providers init-firewall.sh knows (`provider <name> [region]`), with the
// variables that point Claude Code at each; {region} is the profile's region.
var providers = map[string]map[string]string{
	"anthropic":  {},
	"bedrock":    {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_REGION": "{region}"},
	"vertex":     {"CLAUDE_CODE_USE_VERTEX": "1", "CLOUD_ML_REGION": "{region}"},
	"openrouter": {"ANTHROPIC_BASE_URL": "https://openrouter.ai/api"},
}

var region = regexp.MustCompile(`^[a-z0-9-]+$`)

// ParseProfile reads and checks a profile. Nothing is created from a profile that fails here.
func ParseProfile(b []byte) (*Profile, error) {
	var p Profile
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	if p.Schema != ProfileSchema {
		return nil, fmt.Errorf("profile: want `profile: %s`, got %q", ProfileSchema, p.Schema)
	}
	if p.Provider == "" {
		p.Provider = "anthropic"
	}
	if _, ok := providers[p.Provider]; !ok {
		return nil, fmt.Errorf("profile: unknown provider %q (anthropic, bedrock, vertex, openrouter)", p.Provider)
	}
	needsRegion := p.Provider == "bedrock" || p.Provider == "vertex"
	switch {
	case needsRegion && p.Region == "":
		return nil, fmt.Errorf("profile: provider %s needs a region", p.Provider)
	case !needsRegion && p.Region != "":
		return nil, fmt.Errorf("profile: provider %s takes no region", p.Provider)
	case p.Region != "" && !region.MatchString(p.Region):
		return nil, fmt.Errorf("profile: bad region %q", p.Region)
	}
	for k, v := range p.Env {
		if err := checkEnvKey(k); err != nil {
			return nil, fmt.Errorf("profile: env: %w", err)
		}
		if v == "" || strings.ContainsAny(v, "'\n") {
			return nil, fmt.Errorf("profile: env %s: the value must be set, on 1 line, without a single quote (')", k)
		}
	}
	for _, k := range p.Secrets {
		if err := checkEnvKey(k); err != nil {
			return nil, fmt.Errorf("profile: secrets: %w", err)
		}
		if _, ok := p.Env[k]; ok {
			return nil, fmt.Errorf("profile: %s is in both env and secrets", k)
		}
	}
	for _, e := range p.Firewall {
		if err := fwEntry(e, e); err != nil {
			return nil, fmt.Errorf("profile: firewall: bad entry %q: %w", e, err)
		}
	}
	return &p, nil
}

// checkEnvKey is env set's check on a variable name.
func checkEnvKey(k string) error {
	if !envKey.MatchString(k) {
		return fmt.Errorf("%q isn't a variable name", k)
	}
	for _, r := range append(envReserved, berthReserved...) {
		if r == k {
			return fmt.Errorf("%s is managed by %s", k, Tool)
		}
	}
	return nil
}

// ProviderLine is the firewall.txt line that picks the model provider's endpoints.
func (p *Profile) ProviderLine() string {
	return strings.TrimSpace("provider " + p.Provider + " " + p.Region)
}

// Vars are the variables the profile sets: the provider's, then env (which wins), sorted by name.
func (p *Profile) Vars() [][2]string {
	all := map[string]string{}
	for k, v := range providers[p.Provider] {
		all[k] = strings.ReplaceAll(v, "{region}", p.Region)
	}
	for k, v := range p.Env {
		all[k] = v
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, all[k]})
	}
	return out
}

// firewallFile is init's firewall.txt for a profile: the template's header, the provider line, and
// the profile's entries (or the template's presets when it lists none).
func (p *Profile) firewallFile(o string) string {
	t := firewallTemplate(o)
	head, presets, _ := strings.Cut(t, "mode on\n")
	head = strings.Replace(head, "Always allowed: Anthropic/Claude, GitHub, npm.", "Always allowed: GitHub, npm, the provider below.", 1)
	var b strings.Builder
	b.WriteString(head + "mode on\n")
	b.WriteString(ops.Respell("# The model provider's endpoints replace Anthropic's (" + Tool + " init --profile):\n"))
	b.WriteString(p.ProviderLine() + "\n")
	if len(p.Firewall) == 0 {
		b.WriteString(presets)
		return b.String()
	}
	b.WriteString("# From the profile:\n")
	for _, e := range p.Firewall {
		b.WriteString(e + "\n")
	}
	return b.String()
}

// readProfile reads a profile from the operator's machine (wherever the org is).
func (a *App) readProfile(file string) (*Profile, error) {
	b, err := a.Operator.FS.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	return ParseProfile(b)
}

// applyProfile sets a new org's variables and writes its setup script (init writes firewall.txt).
func (a *App) applyProfile(o string, p *Profile) error {
	d := path.Join(a.Orgs.Dir, o)
	var keys []string
	for _, kv := range p.Vars() {
		if err := a.setSecret(o, kv[0], kv[1], "'"+kv[1]+"'"); err != nil {
			return err
		}
		keys = append(keys, kv[0])
	}
	if len(keys) > 0 {
		if err := a.Orgs.Set(o, contract.EnvKeys, strings.Join(keys, " ")); err != nil {
			return err
		}
	}
	if p.Setup != "" {
		if err := a.Host.FS.WriteFile(path.Join(d, "config", path.Base(contract.SetupScript)), []byte(p.Setup), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// errProfileFlag is init's error for --profile without a file.
var errProfileFlag = errors.New("--profile needs a file")
