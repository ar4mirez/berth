package integration

import "testing"

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
