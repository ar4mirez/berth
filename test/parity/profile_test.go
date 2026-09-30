package parity

import (
	"strings"
	"testing"
)

const bedrockProfile = `profile: berth.profile/v1
provider: bedrock
region: us-east-1
env:
  CLAUDE_CODE_ENABLE_TELEMETRY: "1"
  OTEL_METRICS_EXPORTER: otlp
secrets: [AWS_BEARER_TOKEN_BEDROCK]
firewall: ["@node", github.com, 10.0.0.0/8]
setup: |
  echo set up
`

// TestBerthInitProfile: init --profile (berth-only, PARITY.md) writes the provider line and the
// profile's entries to firewall.txt, sets the provider's and the profile's variables as env set
// does, and writes the setup script. A bad profile creates nothing.
func TestBerthInitProfile(t *testing.T) {
	r := run(t, Berth(berthBin), Scenario{Args: []string{"init", "t-new", "--profile", "profile.yaml"}, Rules: nothingRunning,
		Random: newOrgRandom, Files: map[string]File{"home/profile.yaml": {Content: bedrockProfile}}})
	if r.Exit != 0 {
		t.Fatalf("init: exit %d\n%s", r.Exit, r.Stderr)
	}
	tree := strings.Join(r.Tree, "\n")
	for _, want := range []string{
		`state/orgs/t-new/config/firewall.txt 0644 "# Egress allowlist for t-new`,
		`mode on\n# The model provider's endpoints replace Anthropic's (<tool> init --profile):\nprovider bedrock us-east-1\n# From the profile:\n@node\ngithub.com\n10.0.0.0/8\n"`,
		`state/orgs/t-new/config/setup.sh 0644 "echo set up\n"`,
		`CLAUDE_CODE_ENABLE_TELEMETRY='1'\n`,
		`OTEL_METRICS_EXPORTER='otlp'\n`,
		`CLAUDE_CODE_USE_BEDROCK='1'\n`,
		`AWS_REGION='us-east-1'\n`,
		`CCENV_ENV_KEYS=AWS_REGION CLAUDE_CODE_ENABLE_TELEMETRY CLAUDE_CODE_USE_BEDROCK OTEL_METRICS_EXPORTER\n`,
	} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree lacks %s:\n%s", want, tree)
		}
	}
	for _, want := range []string{"From the profile (model provider: bedrock):", "Set AWS_BEARER_TOKEN_BEDROCK:  <tool> env t-new set AWS_BEARER_TOKEN_BEDROCK"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, r.Stdout)
		}
	}

	r = run(t, Berth(berthBin), Scenario{Args: []string{"init", "t-new", "--profile", "profile.yaml"}, Rules: nothingRunning,
		Files: map[string]File{"home/profile.yaml": {Content: "profile: berth.profile/v1\nprovider: vertex\n"}}})
	if r.Exit != 1 || !strings.Contains(r.Stderr, "provider vertex needs a region") {
		t.Errorf("bad profile: exit %d\n%s", r.Exit, r.Stderr)
	}
	for _, l := range r.Tree {
		if strings.HasPrefix(l, "state/orgs/t-new") {
			t.Errorf("a bad profile created %s", l)
		}
	}
}
