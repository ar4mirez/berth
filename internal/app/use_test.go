package app

import "testing"

func TestOrgMissing(t *testing.T) {
	verbs := []string{"show", "allow"}
	for _, c := range []struct {
		x       string
		present bool
		want    bool
	}{
		{"", false, true}, {"--take-lease", true, true}, {"show", true, true}, {"owner/repo", true, true},
		{"summarize the PRs", true, true}, {"acme", true, false}, {"acmee", true, false}, {"acme@box1", true, false},
	} {
		if got := OrgMissing(c.x, c.present, verbs); got != c.want {
			t.Errorf("%q (%v): %v", c.x, c.present, got)
		}
	}
}
