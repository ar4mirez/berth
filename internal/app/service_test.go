package app

import (
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
)

// TestSystemUnit: the whole machine's unit runs berth serve as the account that ran sudo, with
// its home and no --home, so berth finds that account's state root itself (#63).
func TestSystemUnit(t *testing.T) {
	a := New(config.State{Home: config.Home{Path: "/root/.local/share/berth"}}, local.New(), nil, nil, nil, func(k string) string {
		return map[string]string{"DOCKER_HOST": "unix:///run/docker.sock"}[k]
	})
	a.Invoked = "/home/ops/.local/bin/berth"
	u := a.systemUnit("ops", "/home/ops", []string{"--listen", "127.0.0.1:8443"})
	for _, want := range []string{"\n[Service]\nUser=ops\nExecStart=/home/ops/.local/bin/berth serve --listen 127.0.0.1:8443\n",
		"Environment=HOME=/home/ops\n", "Environment=DOCKER_HOST=unix:///run/docker.sock\n", "After=network-online.target docker.service\n", "WantedBy=multi-user.target\n"} {
		if !strings.Contains(u, want) {
			t.Errorf("the unit lacks %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "--home") {
		t.Errorf("the unit names a state root:\n%s", u)
	}
	if p := PackagedUnit(); !strings.Contains(p, "\nExecStart=/usr/bin/berth serve\n") || !strings.Contains(p, "WantedBy=default.target") {
		t.Errorf("the packaged unit:\n%s", p)
	}
}
