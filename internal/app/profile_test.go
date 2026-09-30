package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseProfile(t *testing.T) {
	p, err := ParseProfile([]byte(`profile: berth.profile/v1
provider: bedrock
region: us-east-1
env:
  CLAUDE_CODE_ENABLE_TELEMETRY: "1"
  AWS_REGION: us-west-2
secrets: [AWS_BEARER_TOKEN_BEDROCK]
firewall: ["@node", github.com, 10.0.0.0/8]
setup: |
  echo hi
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.ProviderLine(); got != "provider bedrock us-east-1" {
		t.Errorf("ProviderLine = %q", got)
	}
	// The provider's variables, then env, which wins (AWS_REGION).
	if got := fmt.Sprint(p.Vars()); got != "[[AWS_REGION us-west-2] [CLAUDE_CODE_ENABLE_TELEMETRY 1] [CLAUDE_CODE_USE_BEDROCK 1]]" {
		t.Errorf("Vars = %s", got)
	}
	fw := p.firewallFile("t-a")
	if !strings.HasSuffix(fw, "mode on\n# The model provider's endpoints replace Anthropic's (berth init --profile):\nprovider bedrock us-east-1\n# From the profile:\n@node\ngithub.com\n10.0.0.0/8\n") {
		t.Errorf("firewall.txt:\n%s", fw)
	}
	if !strings.HasPrefix(fw, "# Egress allowlist for t-a") || strings.Contains(fw, "@mise") {
		t.Errorf("firewall.txt keeps the header, not the template's presets:\n%s", fw)
	}

	// No provider is Anthropic, and no firewall list keeps the template's presets.
	p, err = ParseProfile([]byte("profile: berth.profile/v1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if fw := p.firewallFile("t-a"); !strings.Contains(fw, "provider anthropic\n# Toolchain installs via mise") || len(p.Vars()) != 0 {
		t.Errorf("default profile: vars %v, firewall.txt:\n%s", p.Vars(), fw)
	}
	p, _ = ParseProfile([]byte("profile: berth.profile/v1\nprovider: openrouter\n"))
	if got := fmt.Sprint(p.Vars()); got != "[[ANTHROPIC_BASE_URL https://openrouter.ai/api]]" {
		t.Errorf("openrouter Vars = %s", got)
	}
}

func TestParseProfileRefuses(t *testing.T) {
	for _, c := range []struct{ yaml, want string }{
		{"provider: anthropic\n", "want `profile: berth.profile/v1`"},
		{"profile: berth.profile/v2\n", "want `profile: berth.profile/v1`"},
		{"profile: berth.profile/v1\nproviders: bedrock\n", "field providers not found"},
		{"profile: berth.profile/v1\nprovider: azure\n", `unknown provider "azure"`},
		{"profile: berth.profile/v1\nprovider: vertex\n", "provider vertex needs a region"},
		{"profile: berth.profile/v1\nprovider: openrouter\nregion: us\n", "provider openrouter takes no region"},
		{"profile: berth.profile/v1\nprovider: bedrock\nregion: US East\n", `bad region "US East"`},
		{"profile: berth.profile/v1\nenv: {lower: x}\n", `"lower" isn't a variable name`},
		{"profile: berth.profile/v1\nenv: {SSH_PORT: \"1\"}\n", "SSH_PORT is managed by berth"},
		{"profile: berth.profile/v1\nenv: {CLAUDE_ENV_IMAGE: x}\n", "CLAUDE_ENV_IMAGE is managed by berth"},
		{"profile: berth.profile/v1\nenv: {A: \"it's\"}\n", "without a single quote"},
		{"profile: berth.profile/v1\nenv: {A: \"\"}\n", "the value must be set"},
		{"profile: berth.profile/v1\nenv: {A: x}\nsecrets: [A]\n", "A is in both env and secrets"},
		{"profile: berth.profile/v1\nfirewall: [\"mode off\"]\n", `bad entry "mode off"`},
		{"profile: berth.profile/v1\nfirewall: [\"a.example # x\"]\n", "bad entry"},
	} {
		if _, err := ParseProfile([]byte(c.yaml)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: error %v, want %q", c.yaml, err, c.want)
		}
	}
}
