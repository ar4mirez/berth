package ops

import (
	"strings"
	"testing"
)

// TestCheckTool: what a pinned tool may be (#216, docs/api.md, pinned tools): a source mise
// verifies (github: or aqua:, owner/repo) and one exact version, never a range or latest.
func TestCheckTool(t *testing.T) {
	for _, tt := range []struct {
		t    PinnedTool
		want string
	}{
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "1.4.2"}, ""},
		{PinnedTool{Name: "gadget", Source: "aqua:acme/gadget", Version: "v0.6.0"}, ""},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "1.0.0-rc.1"}, ""},
		{PinnedTool{Name: "Gadget", Source: "github:acme/gadget", Version: "1.4.2"}, "lowercase"},
		{PinnedTool{Name: "gadget", Source: "npm:gadget", Version: "1.4.2"}, "verifies"},
		{PinnedTool{Name: "gadget", Source: "github:acme", Version: "1.4.2"}, "verifies"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget;id", Version: "1.4.2"}, "verifies"},
		{PinnedTool{Name: "gadget", Source: "github:../gadget", Version: "1.4.2"}, "verifies"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "latest"}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "^1.4"}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "1.x"}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: ""}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "1"}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "1.4"}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "v1.4"}, "exact version"},
		{PinnedTool{Name: "gadget", Source: "github:acme/gadget", Version: "1.4.2.1"}, ""},
	} {
		err := CheckTool(tt.t)
		if (tt.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%+v: %v, want %q", tt.t, err, tt.want)
		}
	}
}

// TestParseToolsStatus: berth-tools' status file, as image/berth-tools writes it.
func TestParseToolsStatus(t *testing.T) {
	res, done := ParseToolsStatus("gadget github:acme/gadget@1.4.2 ok\nwidget github:acme/widget@2.0.0 failed attestation not found for widget\ndone\n")
	if !done || !res["gadget"].OK || res["widget"].OK || res["widget"].Reason != "attestation not found for widget" || res["gadget"].Spec != "github:acme/gadget@1.4.2" {
		t.Errorf("%v %v", res, done)
	}
	if _, done := ParseToolsStatus("gadget github:acme/gadget@1.4.2 ok\n"); done {
		t.Error("done without its line")
	}
}
