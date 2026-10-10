package parity

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/apiclient"
)

// uiSession runs `berth ui --no-open` (with global flags) against the parity fixtures and returns a
// client for its address, the token of its link, and what it printed on stderr.
func uiSession(t *testing.T, files map[string]File, rules []Rule, global ...string) (*apiclient.Client, string, *syncBuffer, func() Result) {
	t.Helper()
	tool := allCalls()
	command := tool.Command
	tool.Command = func(run string, args []string) []string {
		return append(append(append(command(run, nil), global...), "ui"), args...)
	}
	sess, err := env.Start(t.TempDir(), tool, Scenario{Args: []string{"--no-open"}, Files: files, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	out, errOut := &syncBuffer{}, &syncBuffer{}
	sess.Cmd.Stdout, sess.Cmd.Stderr = out, errOut
	if err := sess.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Cmd.Process.Signal(os.Interrupt)
		_ = sess.Cmd.Wait()
	})
	link := regexp.MustCompile(`^(http://127\.0\.0\.1:\d+)/#token=(berth_[A-Za-z0-9_-]{43})\n$`)
	for i := 0; ; i++ {
		if m := link.FindStringSubmatch(out.String()); m != nil {
			return &apiclient.Client{Base: m[1], HTTP: &http.Client{}}, m[2], errOut, func() Result {
				t.Helper()
				r, err := sess.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				return r
			}
		} else if i > 200 {
			t.Fatalf("berth ui printed no link: stdout %q, stderr %q", out.String(), errOut.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestBerthUI (#167): `berth ui` serves the page to anyone on this machine and the API only to
// whoever has the link's token; it listens on 127.0.0.1; and --read-only holds in it.
func TestBerthUI(t *testing.T) {
	c, token, errOut, snap := uiSession(t, twoOrgs, mcpRules)
	bearer := []string{"Authorization", "Bearer " + token}
	if !strings.Contains(errOut.String(), "berth ui: serving on 127.0.0.1:") || strings.Contains(errOut.String(), token) {
		t.Errorf("stderr: %q", errOut.String())
	}
	// The page and its files need no token: they hold no data.
	for _, path := range []string{"/", "/app.js", "/app.css", "/vendor/alpine-csp.min.js"} {
		if status, body := raw(t, c, "GET", path, ""); status != 200 || body == "" {
			t.Errorf("GET %s: %d, %d bytes", path, status, len(body))
		}
	}
	if status, body := raw(t, c, "GET", "/", ""); status != 200 || !strings.Contains(body, `x-data="app"`) {
		t.Errorf("GET /: %d %.80s", status, body)
	}
	// Everything else does: no token, a wrong one, another run's.
	for _, header := range [][]string{nil, {"Authorization", "Bearer berth_nope"}, {"Authorization", "Bearer "}, {"Authorization", token}} {
		for _, path := range []string{"/v1", "/v1/orgs", "/v1/orgs/acme"} {
			if status, body := raw(t, c, "GET", path, "", header...); status != 401 || strings.Contains(body, "acme") {
				t.Errorf("GET %s with %v: %d %s", path, header, status, body)
			}
		}
	}
	if status, _ := raw(t, c, "POST", "/v1/orgs/acme/restart", `{"confirm":"acme"}`); status != 401 {
		t.Errorf("a restart with no token: %d", status)
	}
	if len(snap().Calls) != 0 {
		t.Errorf("requests without the token ran something: %v", snap().Calls)
	}
	// With the token: whoever ran berth ui, who may do what berth does.
	if status, body := raw(t, c, "GET", "/v1", "", bearer...); status != 200 || !strings.Contains(body, `"writes": true`) || !strings.Contains(body, `"restarts": true`) {
		t.Errorf("GET /v1: %d %s", status, body)
	}
	if status, body := raw(t, c, "GET", "/v1/orgs", "", bearer...); status != 200 || !strings.Contains(body, `"name": "acme"`) {
		t.Errorf("GET /v1/orgs: %d %s", status, body)
	}
	// A restart still needs the org's name again.
	if status, body := raw(t, c, "POST", "/v1/orgs/acme/restart", `{}`, bearer...); status != 403 || !strings.Contains(body, "stops the work running in acme") {
		t.Errorf("a restart without confirm: %d %s", status, body)
	}
	// What isn't a file or an endpoint is neither.
	if status, _ := raw(t, c, "GET", "/nope", ""); status != 404 {
		t.Errorf("GET /nope: %d", status)
	}
	if status, body := raw(t, c, "GET", "/v1/nope", "", bearer...); status != 404 || !strings.Contains(body, "berth.error/v1") {
		t.Errorf("GET /v1/nope: %d %s", status, body)
	}

	ro, roToken, roErr, roSnap := uiSession(t, twoOrgs, mcpRules, "--read-only")
	roBearer := []string{"Authorization", "Bearer " + roToken}
	if roToken == token || !strings.Contains(roErr.String(), "(read-only)") {
		t.Errorf("a second run: the same token, or not read-only: %q", roErr.String())
	}
	if status, body := raw(t, ro, "GET", "/v1", "", roBearer...); status != 200 || !strings.Contains(body, `"writes": false`) || !strings.Contains(body, `"read_only": true`) {
		t.Errorf("GET /v1, read-only: %d %s", status, body)
	}
	before := len(roSnap().Calls)
	if status, body := raw(t, ro, "POST", "/v1/orgs/acme/firewall/allow", `{"entries":["files.example.com"]}`, roBearer...); status != 403 {
		t.Errorf("a write, read-only: %d %s", status, body)
	}
	if after := roSnap().Calls; len(after) != before {
		t.Errorf("a refused write ran something: %v", after[before:])
	}
	// One run's token is nothing to another.
	if status, _ := raw(t, ro, "GET", "/v1/orgs", "", bearer...); status != 401 {
		t.Errorf("the first run's token on the second: %d", status)
	}
}

// TestBerthServeWebUI (#167): `berth serve` has the page beside the API, on the socket and on TCP,
// where the page needs no token and the API still does; --no-ui leaves it out.
func TestBerthServeWebUI(t *testing.T) {
	tokens := map[string]string{"read": "berth_READ-fixture-token"}
	files := merge(twoOrgs, map[string]File{"home/.config/berth/api/tokens.json": {Content: tokenFile(tokens), Mode: 0o600}})
	c, _, _ := apiSessionArgs(t, files, mcpRules, []string{"--listen", "127.0.0.1:0"})
	var tcp *apiclient.Client
	for i := 0; i < 100 && tcp == nil; i++ {
		if m := regexp.MustCompile(`https://(127\.0\.0\.1:\d+) \(TLS, tokens; certificate sha256 ([0-9a-f]{64})\)`).FindStringSubmatch(serveStderr.String()); m != nil {
			tcp = apiclient.TCP(m[1], "", m[2])
		}
		time.Sleep(20 * time.Millisecond)
	}
	if tcp == nil {
		t.Fatalf("no TCP address in: %s", serveStderr.String())
	}
	for name, client := range map[string]*apiclient.Client{"socket": c, "tcp": tcp} {
		if status, body := raw(t, client, "GET", "/", ""); status != 200 || !strings.Contains(body, `x-data="app"`) {
			t.Errorf("%s: GET /: %d %.80s", name, status, body)
		}
		if status, body := raw(t, client, "POST", "/", "{}"); status != 404 || !strings.Contains(body, "berth.error/v1") {
			t.Errorf("%s: POST /: %d %s", name, status, body)
		}
	}
	if status, _ := raw(t, tcp, "GET", "/v1/orgs", ""); status != 401 {
		t.Errorf("tcp: GET /v1/orgs with no token: %d", status)
	}
	if status, body := raw(t, tcp, "GET", "/v1/orgs", "", "Authorization", "Bearer "+tokens["read"]); status != 200 || !strings.Contains(body, `"name": "acme"`) {
		t.Errorf("tcp: GET /v1/orgs: %d %s", status, body)
	}

	bare, _, _ := apiSessionArgs(t, twoOrgs, mcpRules, []string{"--no-ui"})
	for _, path := range []string{"/", "/app.js"} {
		if status, body := raw(t, bare, "GET", path, ""); status != 404 || !strings.Contains(body, "berth.error/v1") {
			t.Errorf("--no-ui: GET %s: %d %s", path, status, body)
		}
	}
	if status, _ := raw(t, bare, "GET", "/v1/orgs", ""); status != 200 {
		t.Errorf("--no-ui: GET /v1/orgs: %d", status)
	}
}
