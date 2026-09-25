package app

import (
	"strings"
	"testing"
)

// These are what real `jq -r` 1.6 printed for ccenv's whoami program, and whether it exited
// non-zero. berth reproduces them without needing jq on the host.
func TestAuthStatusLines(t *testing.T) {
	const out = "LOGGEDOUT"
	tests := []struct {
		in      string
		want    string // lines joined by |
		wantErr bool
	}{
		{`{"loggedIn":true,"email":"dev@example.com","orgName":"Acme"}`, "dev@example.com  [Acme]", false},
		{`{"loggedIn":false}`, out, false},
		{`null`, out, false},
		{``, "", false},
		{`{"loggedIn":"no"}`, "null  [null]", false},
		{`{"loggedIn":1,"orgName":{"n":"<x>"},"email":1.50}`, `1.5  [{"n":"<x>"}]`, false},
		{`{"loggedIn":false} 42 {"loggedIn":true}`, out + "|null  [null]", false},
		{`42`, "", true},
		{`"str"`, "", true},
		{`[1]`, "", true},
		{`true`, "", true},
		{`garbage`, "", true},
		{`{"loggedIn":true} garbage {"loggedIn":false}`, "null  [null]", true},
	}
	for _, tt := range tests {
		lines, err := authStatusLines([]byte(tt.in), out)
		if got := strings.Join(lines, "|"); got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("%s: got %q, err=%v; want %q, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}
