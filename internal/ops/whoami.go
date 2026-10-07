package ops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// GetWhoami is `whoami [org...]`: which Claude account (and GitHub account) each org uses; every
// org when none is named. An unknown org ends it there: the orgs before it are returned with the
// error, because ccenv has printed them by then.
func GetWhoami(ctx context.Context, s System, orgs []string) (Whoami, error) {
	out := Whoami{Schema: "berth.whoami/v1", Orgs: []Account{}}
	if len(orgs) == 0 {
		// ccenv's loop can end with a failed [ -f ] (a dir without org.env last, or no orgs), but bash
		// doesn't exit for a compound command that failed where set -e was ignored: no quirk here.
		for _, d := range s.OrgDirs() {
			if s.IsFile(EnvPath(s, d)) {
				orgs = append(orgs, d)
			}
		}
	}
	for _, o := range orgs {
		if err := NeedOrg(s, o); err != nil {
			return out, err
		}
		a := Account{Org: o, GitHubText: "-", ClaudeText: "(container down)"}
		if Running(ctx, s, o) {
			a.Running = true
			a.Claude, a.ClaudeText = claudeAccount(ctx, s, o)
			a.GitHub, _ = s.Capture(ctx, false, "docker", "exec", "-u", "node", contract.Container(o), "sh", "-c",
				"gh api user -q .login 2>/dev/null; true")
			if a.GitHubText = a.GitHub; a.GitHub == "" {
				a.GitHubText = "not signed in"
			}
		}
		a.Token = s.Secret(o, "CLAUDE_CODE_OAUTH_TOKEN") != ""
		out.Orgs = append(out.Orgs, a)
	}
	return out, nil
}

// claudeAccount is
//
//	st=$(docker exec -u node claude-$o sh -c 'env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status 2>/dev/null; true' \
//	     | jq -r 'if .loggedIn then "\(.email)  [\(.orgName)]" else "not logged in (ccenv login $o)" end' 2>/dev/null \
//	     || echo "not logged in")
//
// With jq replaced by AuthStatusLines. Under pipefail, a docker failure also appends "not logged in"
// to whatever jq printed. The account itself is the status's first value, when it is logged in.
func claudeAccount(ctx context.Context, s System, org string) (*ClaudeAccount, string) {
	var out bytes.Buffer
	status, dockerErr := s.CaptureRaw(ctx, false, "docker", "exec", "-u", "node", contract.Container(org), "sh", "-c",
		"env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status 2>/dev/null; true")
	ls, jqErr := AuthStatusLines([]byte(status), Respell("not logged in ("+Tool+" login "+org+")"))
	for _, l := range ls {
		out.WriteString(l + "\n")
	}
	if dockerErr != nil || jqErr != nil {
		out.WriteString("not logged in\n")
	}
	var acct *ClaudeAccount
	var v struct {
		LoggedIn bool   `json:"loggedIn"`
		Email    string `json:"email"`
		OrgName  string `json:"orgName"`
		OrgID    string `json:"orgId"`
	}
	if dockerErr == nil && json.NewDecoder(strings.NewReader(status)).Decode(&v) == nil && v.LoggedIn {
		acct = &ClaudeAccount{Email: v.Email, Organization: v.OrgName, OrganizationID: v.OrgID}
	}
	return acct, strings.TrimRight(out.String(), "\n")
}

// AuthStatusLines evaluates whoami's jq program,
// `if .loggedIn then "\(.email)  [\(.orgName)]" else "<loggedOut>" end`, like `jq -r` (1.6).
func AuthStatusLines(input []byte, loggedOut string) ([]string, error) {
	return jqEach(input, func(field func(string) any) (string, bool) {
		if l := field("loggedIn"); l == nil || l == false {
			return loggedOut, true
		}
		return JQString(field("email")) + "  [" + JQString(field("orgName")) + "]", true
	})
}

// AccountLines evaluates check_account's jq program,
// `select(.loggedIn) | "\(.orgId) \(.email) [\(.orgName)]"`.
func AccountLines(input []byte) ([]string, error) {
	return jqEach(input, func(field func(string) any) (string, bool) {
		if l := field("loggedIn"); l == nil || l == false {
			return "", false
		}
		return JQString(field("orgId")) + " " + JQString(field("email")) + " [" + JQString(field("orgName")) + "]", true
	})
}

// jqEach runs a per-value jq program over a stream of JSON values, like `jq -r` (1.6): one output
// line per value that emits one. A value that can't be indexed (a number, string, array, bool) is an
// error that jq reports and moves past; the exit status is that of the last value. A parse error
// stops jq. Numbers print as doubles (1.50 → 1.5).
func jqEach(input []byte, program func(field func(string) any) (string, bool)) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(input))
	var lines []string
	var last error
	for {
		var v any
		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return lines, last
		}
		if err != nil {
			return lines, err
		}
		last = nil
		m, isObject := v.(map[string]any)
		if !isObject && v != nil {
			last = fmt.Errorf("cannot index %T", v)
			continue
		}
		if line, emit := program(func(name string) any { return m[name] }); emit {
			lines = append(lines, line)
		}
	}
}

// JQString is jq's string interpolation of a value: strings raw, anything else as compact JSON.
func JQString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}
