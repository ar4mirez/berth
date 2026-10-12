package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/tui"
)

// TestDashboardRunsTools (#167): the terminal dashboard's backend, on a state root of its own with
// no orgs, runs operations as the tools the API serves: a read answers with its document; a write
// reaches its command, whose own refusal comes back; --read-only refuses it before that; and what
// was asked for is in the audit log, as from "tui".
func TestDashboardRunsTools(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	st := config.State{Home: config.Home{Path: home, Source: config.SourceFlag}}
	ctx := context.Background()

	b := tui.NewBackend(appMaker(st), false, "", nil)
	if out, err := b.Invoke(ctx, "orgs_list", nil); err != nil || !strings.Contains(out, `"schema": "berth.orgs/v1"`) {
		t.Errorf("orgs_list: %q %v", out, err)
	}
	if _, err := b.Invoke(ctx, "no_such_tool", nil); err == nil {
		t.Error("a tool that doesn't exist ran")
	}
	// A key's action is the same tool: its command says there is no such org.
	_, err := b.Run(ctx, tui.Action{Cmd: "fw", Sub: "allow", Org: "t-none", Args: []string{"files.example.com"}})
	if err == nil || !strings.Contains(err.Error(), "t-none") {
		t.Errorf("fw allow on an org that isn't there: %v", err)
	}
	// A restart with a key was confirmed on screen: the tool gets the org's name again, and runs.
	_, err = b.Run(ctx, tui.Action{Cmd: "restart", Org: "t-none"})
	if err == nil || strings.Contains(err.Error(), "confirm") || !strings.Contains(err.Error(), "t-none") {
		t.Errorf("restart on an org that isn't there: %v", err)
	}

	ro := tui.NewBackend(appMaker(config.State{Home: st.Home, ReadOnly: true}), true, "", nil)
	if _, err := ro.Invoke(ctx, "firewall_allow", map[string]any{"org": "t-none", "entries": []string{"x.example"}}); err == nil || !strings.Contains(err.Error(), "may only read") {
		t.Errorf("a write, read-only: %v", err)
	}
	if _, err := ro.Invoke(ctx, "org_destroy", map[string]any{"org": "t-none", "confirm": "t-none"}); err == nil || !strings.Contains(err.Error(), "admin scope") {
		t.Errorf("a destroy, read-only: %v", err)
	}
	if out, err := ro.Invoke(ctx, "hosts_list", nil); err != nil || !strings.Contains(out, `"name": "local"`) {
		t.Errorf("a read, read-only: %q %v", out, err)
	}

	log, err := os.ReadFile(filepath.Join(home, "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"via":"tui"`, `"tool":"firewall_allow"`, `"outcome":"failed"`, `"tool":"org_restart"`} {
		if !strings.Contains(string(log), want) {
			t.Errorf("the audit log lacks %s:\n%s", want, log)
		}
	}
}
