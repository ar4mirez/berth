package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Whoami is `ccenv whoami [org...]`: which Claude account (and GitHub account) each org uses.
func (a *App) Whoami(ctx context.Context, orgs []string) error {
	if len(orgs) == 0 {
		// ccenv's loop can end with a failed [ -f ] (a dir without org.env last, or no orgs), but bash
		// doesn't exit for a compound command that failed where set -e was ignored: no quirk here.
		for _, d := range a.orgDirs() {
			if a.isFile(a.Orgs.EnvPath(d)) {
				orgs = append(orgs, d)
			}
		}
	}
	fmt.Fprintf(a.Stdout, "%-12s %-7s %-14s %s\n", "ORG", "TOKEN", "GITHUB (gh)", "REMOTE-CONTROL LOGIN (account)")
	for _, o := range orgs {
		if err := a.needOrg(o); err != nil {
			return err
		}
		st, gh := "(container down)", "-"
		if a.running(ctx, o) {
			st = a.claudeAccount(ctx, o)
			if gh, _ = a.capture(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "sh", "-c",
				"gh api user -q .login 2>/dev/null; true"); gh == "" {
				gh = "not signed in"
			}
		}
		token := "MISSING"
		if a.env(o, "CLAUDE_CODE_OAUTH_TOKEN") != "" {
			token = "set"
		}
		fmt.Fprintf(a.Stdout, "%-12s %-7s %-14s %s\n", o, token, gh, st)
	}
	return nil
}

// claudeAccount is
//
//	st=$(docker exec -u node claude-$o sh -c 'env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status 2>/dev/null; true' \
//	     | jq -r 'if .loggedIn then "\(.email)  [\(.orgName)]" else "not logged in (ccenv login $o)" end' 2>/dev/null \
//	     || echo "not logged in")
//
// With jq replaced by authStatusLines. Under pipefail, a docker failure also appends "not logged in"
// to whatever jq printed.
func (a *App) claudeAccount(ctx context.Context, o string) string {
	var out bytes.Buffer
	status, dockerErr := a.captureRaw(ctx, false, "docker", "exec", "-u", "node", "claude-"+o, "sh", "-c",
		"env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status 2>/dev/null; true")
	lines, jqErr := authStatusLines([]byte(status), "not logged in ("+Tool+" login "+o+")")
	for _, l := range lines {
		out.WriteString(l + "\n")
	}
	if dockerErr != nil || jqErr != nil {
		out.WriteString("not logged in\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

// authStatusLines evaluates whoami's jq program,
// `if .loggedIn then "\(.email)  [\(.orgName)]" else "<loggedOut>" end`, like `jq -r` (1.6).
func authStatusLines(input []byte, loggedOut string) ([]string, error) {
	return jqEach(input, func(field func(string) any) (string, bool) {
		if l := field("loggedIn"); l == nil || l == false {
			return loggedOut, true
		}
		return jqString(field("email")) + "  [" + jqString(field("orgName")) + "]", true
	})
}

// accountLines evaluates check_account's jq program,
// `select(.loggedIn) | "\(.orgId) \(.email) [\(.orgName)]"`.
func accountLines(input []byte) ([]string, error) {
	return jqEach(input, func(field func(string) any) (string, bool) {
		if l := field("loggedIn"); l == nil || l == false {
			return "", false
		}
		return jqString(field("orgId")) + " " + jqString(field("email")) + " [" + jqString(field("orgName")) + "]", true
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

// jqString is jq's string interpolation of a value: strings raw, anything else as compact JSON.
func jqString(v any) string {
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
