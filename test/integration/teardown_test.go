package integration

import (
	"strings"
	"testing"
)

func TestKnownRootlessTeardown(t *testing.T) {
	const known = ` Container claude-t-guard Stopping
 Container claude-t-guard Error Error while Stopping
Error response from daemon: removing container 5dfa4ad8dcfe network: 1 error occurred:
	* rootless netns: kill network process: permission denied
Error: executing /usr/local/bin/docker-compose -f /home/podman/.local/share/berth/berth/compose.yml --env-file org.env down: exit status 1
`
	tests := []struct {
		name string
		out  string
		want bool
	}{
		{"the known error", known, true},
		{"empty", "", false},
		{"another network error", `Error response from daemon: removing container 5dfa network: 1 error occurred:
	* rootless netns: something else
Error: executing /usr/local/bin/docker-compose down: exit status 1
`, false},
		{"the known error and another", `Error response from daemon: removing container 5dfa network: 2 errors occurred:
	* rootless netns: kill network process: permission denied
	* netavark: iptables failed
Error: executing /usr/local/bin/docker-compose down: exit status 1
`, false},
		{"a count that doesn't match", `Error response from daemon: removing container 5dfa network: 2 errors occurred:
	* rootless netns: kill network process: permission denied
Error: executing /usr/local/bin/docker-compose down: exit status 1
`, false},
		{"another daemon error too", known + "Error response from daemon: no such container\n", false},
		{"the message, but not from network removal", `Error response from daemon: stopping container 5dfa: 1 error occurred:
	* rootless netns: kill network process: permission denied
`, false},
		{"a berth error", known + "berth: Error: compose failed\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := knownRootlessTeardown(tt.out); got != tt.want {
				t.Errorf("knownRootlessTeardown = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestUpstreamTeardown: the known error is tolerated only with the kernel's record of its cause,
// made during the down. The denial is the line a runner's kernel logged when it happened (#215).
func TestUpstreamTeardown(t *testing.T) {
	const known = `Error response from daemon: removing container 5dfa4ad8dcfe network: 1 error occurred:
	* rootless netns: kill network process: permission denied
Error: executing /usr/local/bin/docker-compose -f compose.yml --env-file org.env down: exit status 1
`
	const denial = `[  186.166668] audit: type=1400 audit(1795801521.584:203): apparmor="DENIED" operation="signal" class="signal" profile="pasta" pid=11354 comm="exe" requested_mask="receive" denied_mask="receive" signal=term peer="podman"`
	const earlier = `[  157.770987] audit: type=1400 audit(1795801493.188:192): apparmor="DENIED" operation="open" class="file" profile="pasta" name="/proc/sys/net/netfilter/nf_conntrack_udp_timeout" pid=12474 comm="pasta.avx2" requested_mask="r" denied_mask="r" fsuid=1000`
	other := func(r *strings.Replacer) string { return r.Replace(denial) }
	tests := []struct {
		name, out, before, after string
		want                     string
	}{
		{"the error, and its denial during the down", known, earlier, earlier + "\n" + denial + "\n", denial},
		{"the error, with no denial", known, earlier, earlier, ""},
		{"the error, and only a denial from before the down", known, denial, denial, ""},
		{"a second denial, during the down", known, denial, denial + "\n" + denial, denial},
		{"the denial, but another error", "Error response from daemon: no such container\n", "", denial, ""},
		{"another peer", known, "", other(strings.NewReplacer(`peer="podman"`, `peer="unconfined"`)), ""},
		{"another signal", known, "", other(strings.NewReplacer("signal=term", "signal=kill")), ""},
		{"another profile", known, "", other(strings.NewReplacer(`profile="pasta"`, `profile="slirp4netns"`)), ""},
		{"no kernel log", known, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := upstreamTeardown(tt.out, tt.before, tt.after); got != tt.want {
				t.Errorf("upstreamTeardown = %q, want %q", got, tt.want)
			}
		})
	}
}
