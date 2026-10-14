package host

import (
	"reflect"
	"testing"
)

// TestNewOrgSpec: compose.yml for one org, with variables looked up as compose does (the process's
// environment first, then org.env) and compose.yml's defaults (#43). The integration suite checks
// the same against `docker compose config` (TestOrgSpecMatchesCompose).
func TestNewOrgSpec(t *testing.T) {
	env := map[string]string{"ORG": "acme", "ORG_DIR": "/s/orgs/acme", "BIND_ADDR": "100.64.0.7", "HOST_UID": "1000", "HOST_GID": "1000", "CPUS": "2"}
	org := map[string]string{"SSH_PORT": "2201", "TTYD_PORT": "7601", "BIND_ADDR": "0.0.0.0", "CPUS": "6", "MEM_LIMIT": "4g"}
	s, err := NewOrgSpec(env, org, "/s/orgs/acme/org.env")
	if err != nil {
		t.Fatal(err)
	}
	want := OrgSpec{
		Project: "claude-acme", Name: "claude-acme", Hostname: "acme",
		Image: "claude-env:latest", BuildContext: "./image",
		BuildArgs: map[string]string{"USER_UID": "1000", "USER_GID": "1000", "CLAUDE_CODE_VERSION": ""},
		EnvFile:   "/s/orgs/acme/org.env",
		Env:       map[string]string{"ORG": "acme", "HOST_UID": "1000", "HOST_GID": "1000"},
		// The process's BIND_ADDR and CPUS win over org.env's, as with compose.
		Ports:  []PortSpec{{"100.64.0.7", "2201", "2222"}, {"100.64.0.7", "7601", "7681"}},
		CapAdd: []string{"NET_ADMIN", "NET_RAW"},
		Memory: "4g", CPUs: "2", Restart: "unless-stopped",
	}
	for _, m := range orgMounts {
		want.Mounts = append(want.Mounts, MountSpec{Source: "/s/orgs/acme/" + m.dir, Target: m.target, ReadOnly: m.ro})
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("got  %+v\nwant %+v", s, want)
	}
	// berth's image, rootless Podman, and compose.yml's defaults for what isn't set.
	s, err = NewOrgSpec(map[string]string{"ORG": "acme", "ORG_DIR": "/d", "CLAUDE_ENV_IMAGE": "berth/claude-env", "IMAGE_TAG": "0123456789ab",
		"CLAUDE_ENV_IMAGE_DIR": "/s/berth/image", "BERTH_USERNS": "keep-id", "BERTH_USER": "0:0"}, map[string]string{"SSH_PORT": "1", "TTYD_PORT": "2"}, "/d/org.env")
	if err != nil || s.Image != "berth/claude-env:0123456789ab" || s.BuildContext != "/s/berth/image" || s.UsernsMode != "keep-id" || s.User != "0:0" ||
		s.Memory != "8g" || s.CPUs != "4" || s.Ports[0].HostIP != "127.0.0.1" || s.BuildArgs["USER_UID"] != "1000" || s.Env["HOST_UID"] != "" {
		t.Errorf("berth's image, rootless, defaults: %v %+v", err, s)
	}
	for _, c := range []struct{ env, org map[string]string }{
		{map[string]string{"ORG_DIR": "/d"}, map[string]string{"SSH_PORT": "1", "TTYD_PORT": "2"}},
		{map[string]string{"ORG": "acme", "ORG_DIR": "/d"}, map[string]string{"SSH_PORT": "1"}},
	} {
		if _, err := NewOrgSpec(c.env, c.org, "/d/org.env"); err == nil {
			t.Errorf("accepted %v %v", c.env, c.org)
		}
	}
}
