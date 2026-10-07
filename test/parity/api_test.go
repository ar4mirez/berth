package parity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ar4mirez/berth/internal/api"
	"github.com/ar4mirez/berth/internal/apiclient"
	"github.com/ar4mirez/berth/internal/ops"
)

// apiSession runs `berth serve` (with global flags) against the parity fixtures, on a socket of its
// own, and returns a client for it: the real binary, over HTTP, with the fake docker behind it.
func apiSession(t *testing.T, files map[string]File, rules []Rule, global ...string) (*apiclient.Client, string, func() Result) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "berth-api-") // a socket's path must be short
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s")
	tool := allCalls()
	command := tool.Command
	tool.Command = func(run string, args []string) []string {
		argv := command(run, nil)
		return append(append(append(argv, global...), "serve"), args...)
	}
	sess, err := env.Start(t.TempDir(), tool, Scenario{Args: []string{"--socket", sock}, Files: files, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	var errOut strings.Builder
	sess.Cmd.Stderr = &errOut
	if err := sess.Cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Cmd.Process.Signal(os.Interrupt)
		_ = sess.Cmd.Wait()
	})
	c := apiclient.Unix(sock)
	for i := 0; ; i++ {
		if _, err := c.Info(context.Background()); err == nil {
			break
		} else if i > 100 {
			t.Fatalf("berth serve didn't start: %v\n%s", err, errOut.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return c, sock, func() Result {
		t.Helper()
		r, err := sess.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
}

// raw is a request without the client: the status, and the body.
func raw(t *testing.T, c *apiclient.Client, method, path, body string, header ...string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, c.Base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func asJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func kindOf(err error) ops.Kind {
	var e *ops.Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return ""
}

// TestBerthAPI: over the socket, every endpoint answers with its document; errors are documents
// with the right status; arguments that don't fit are refused; and the socket is its owner's only.
func TestBerthAPI(t *testing.T) {
	files := merge(jsonRepos, map[string]File{
		"state/backups": {Dir: true},
		"state/backups/acme-20261006-023000.tar.zst.age": {Content: "backup", Mode: 0o600},
		"home/.config/berth/backup.key":                  {Content: "# public key: age1parity\nAGE-SECRET-KEY-1PARITY\n", Mode: 0o600},
	})
	c, sock, snap := apiSession(t, files, append([]Rule{
		{Bin: "docker", Match: `^exec claude-acme test -e /workspace/widget$`, Exit: 1},
		{Bin: "docker", Match: `^image inspect`, Exit: 0},
	}, mcpRules...))
	ctx := context.Background()

	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Errorf("the socket: %v %v", fi, err)
	}
	info, err := c.Info(ctx)
	if err != nil || info.Schema != "berth.api/v1" || info.ReadOnly || !info.Writes || !info.Restarts {
		t.Errorf("GET /v1: %+v %v", info, err)
	}
	if status, body := raw(t, c, "GET", "/v1/openapi.json", ""); status != 200 || body != string(api.OpenAPI()) {
		t.Errorf("GET /v1/openapi.json: %d, and it isn't the committed document", status)
	}

	// Every endpoint, through the generated client. A route this table lacks fails the test.
	calls := map[string]func() (any, error){
		"hosts_list":       func() (any, error) { return c.HostsList(ctx) },
		"orgs_list":        func() (any, error) { return c.OrgsList(ctx) },
		"org_accounts":     func() (any, error) { return c.OrgAccounts(ctx, []string{"acme"}) },
		"backups_list":     func() (any, error) { return c.BackupsList(ctx, "acme") },
		"schedule_status":  func() (any, error) { return c.ScheduleStatus(ctx) },
		"org_info":         func() (any, error) { return c.OrgInfo(ctx, "acme") },
		"remote_status":    func() (any, error) { return c.RemoteStatus(ctx, "acme") },
		"org_logs":         func() (any, error) { return c.OrgLogs(ctx, "acme", 2) },
		"firewall_show":    func() (any, error) { return c.FirewallShow(ctx, "acme") },
		"firewall_presets": func() (any, error) { return c.FirewallPresets(ctx, "acme") },
		"firewall_test":    func() (any, error) { return c.FirewallTest(ctx, "acme", []string{"pypi.org"}) },
		"repos_list":       func() (any, error) { return c.ReposList(ctx, "acme") },
		"env_list":         func() (any, error) { return c.EnvList(ctx, "acme") },
		"packages_list":    func() (any, error) { return c.PackagesList(ctx, "acme") },
		"firewall_allow":   func() (any, error) { return c.FirewallAllow(ctx, "acme", []string{"files.example.com"}) },
		"firewall_deny":    func() (any, error) { return c.FirewallDeny(ctx, "acme", []string{"pypi.org"}) },
		"repo_add":         func() (any, error) { return c.RepoAdd(ctx, "acme", "acme/widget", "", "", false) },
		"repo_remove":      func() (any, error) { return c.RepoRemove(ctx, "acme", "app") },
		"backup_create":    func() (any, error) { return c.BackupCreate(ctx, []string{"acme"}, false) },
		"org_restart":      func() (any, error) { return c.OrgRestart(ctx, "acme", "acme") },
		"org_up":           func() (any, error) { return c.OrgUp(ctx, "acme", "acme") },
		"org_down":         func() (any, error) { return c.OrgDown(ctx, "acme", "acme") },
	}
	want := map[string][]string{
		"hosts_list": {`"schema":"berth.hosts/v1"`}, "orgs_list": {`"schema":"berth.orgs/v1"`, `"name":"acme"`, `"name":"globex"`},
		"org_accounts": {`"github":"octo-acme"`}, "backups_list": {`"file":"acme-20261006-023000.tar.zst.age"`, `"encryption":"age"`},
		"schedule_status": {`"kind":"none"`}, "org_info": {`"schema":"berth.info/v1"`, `"state":"running"`},
		"remote_status": {`"state":"on"`}, "org_logs": {`claude-env[acme]: ready`}, "firewall_show": {`"live":"on (12 entries)"`},
		"firewall_presets": {`"name":"@python"`}, "firewall_test": {`"allowed":true`}, "repos_list": {`"dir":"app"`},
		"env_list": {`"schema":"berth.env/v1"`}, "packages_list": {`"schema":"berth.packages/v1"`},
		"firewall_allow": {`allowed: files.example.com`, `"files.example.com"`}, "firewall_deny": {`removed: pypi.org`},
		"repo_add": {`Registered github.com/acme/widget as /workspace/widget`}, "repo_remove": {`Unregistered /workspace/app`},
		"backup_create": {`Wrote `}, "org_restart": {`"org":"acme"`}, "org_up": {`"org":"acme"`}, "org_down": {`"org":"acme"`},
	}
	for _, r := range api.Routes {
		call, ok := calls[r.Tool]
		if !ok {
			t.Errorf("%s %s (%s): this test doesn't call it", r.Method, r.Path, r.Tool)
			continue
		}
		out, err := call()
		if err != nil {
			t.Errorf("%s %s: %v", r.Method, r.Path, err)
			continue
		}
		got := asJSON(out)
		for _, w := range want[r.Tool] {
			if !strings.Contains(got, w) {
				t.Errorf("%s %s: no %s in %s", r.Method, r.Path, w, got)
			}
		}
		if strings.Contains(got, "sk-ant-") || strings.Contains(got, "PARITY-TOKEN") || strings.Contains(got, "AGE-SECRET") {
			t.Errorf("%s %s returned a secret: %s", r.Method, r.Path, got)
		}
	}
	res := snap()
	if !hasCall(res, `"up" "-d" "--force-recreate"`) || !hasCall(res, `"down"`) {
		t.Errorf("the lifecycle endpoints ran no compose:\n%s", strings.Join(res.Calls, "\n"))
	}
	audit := strings.Join(auditLines(t, res), "\n")
	for _, w := range []string{`firewall_allow|ok|{"entries":["files.example.com"],"org":"acme"}`, `repo_remove|ok|{"dir":"app","org":"acme"}`, `org_restart|ok|{"org":"acme"}`, `backup_create|ok|`} {
		if !strings.Contains(audit, w) {
			t.Errorf("the audit log lacks %s:\n%s", w, audit)
		}
	}
	if auditVia != "api" {
		t.Errorf("the audit log's via is %q", auditVia)
	}

	// Errors are documents, with the status of their kind.
	if _, err := c.OrgInfo(ctx, "nope"); kindOf(err) != ops.KindNotFound || !strings.Contains(err.Error(), "unknown org 'nope'") {
		t.Errorf("an unknown org: %v", err)
	}
	if _, err := c.RemoteStatus(ctx, "globex"); kindOf(err) != ops.KindNotRunning {
		t.Errorf("an org that is down: %v (%s)", err, kindOf(err))
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		want               string
	}{
		{"GET", "/v1/orgs/nope", "", 404, `"kind": "not-found"`},
		{"GET", "/v1/orgs/globex/remote", "", 409, `"kind": "not-running"`},
		{"GET", "/v1/nothing", "", 404, "no GET /v1/nothing in this API"},
		{"PUT", "/v1/orgs", "", 404, "no PUT /v1/orgs in this API"},
		{"GET", "/v1/orgs/acme/logs?lines=many", "", 400, "lines must be a number"},
		{"GET", "/v1/orgs/acme/logs?follow=1", "", 400, `no query parameter \"follow\"`},
		{"POST", "/v1/orgs/acme/firewall/allow", `{"entries":["x.example"],"org":"globex"}`, 400, "org is in the path"},
		{"POST", "/v1/orgs/acme/firewall/allow", `{"entries":["x.example"],"force":true}`, 400, "don't fit firewall_allow"},
		{"POST", "/v1/orgs/acme/firewall/allow", `not json`, 400, "isn't a JSON object"},
		{"POST", "/v1/orgs/acme/firewall/allow", `{"entries":["web-git-*.example.app"]}`, 500, "berth.error/v1"},
		{"POST", "/v1/orgs/acme/repos", `{"repo":"--no-clone"}`, 500, "can't start with '-'"},
	} {
		status, body := raw(t, c, tc.method, tc.path, tc.body)
		if status != tc.status || !strings.Contains(body, tc.want) || !strings.Contains(body, "berth.error/v1") {
			t.Errorf("%s %s %s: %d %s (want %d, %q)", tc.method, tc.path, tc.body, status, body, tc.status, tc.want)
		}
	}
	// A second server on the same socket is refused, and so is a path that isn't a socket.
	out := run(t, Berth(berthBin), Scenario{Args: []string{"serve", "--socket", sock}, Files: twoOrgs})
	if out.Exit != 1 || !strings.Contains(out.Stderr, "already listening") {
		t.Errorf("a second server: exit %d, %q", out.Exit, out.Stderr)
	}
	plain := filepath.Join(filepath.Dir(sock), "file")
	if err := os.WriteFile(plain, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	out = run(t, Berth(berthBin), Scenario{Args: []string{"serve", "--socket", plain}, Files: twoOrgs})
	if b, _ := os.ReadFile(plain); out.Exit != 1 || !strings.Contains(out.Stderr, "isn't a socket") || string(b) != "mine" {
		t.Errorf("a file in the socket's place: exit %d, %q, the file is %q", out.Exit, out.Stderr, b)
	}
}

// TestBerthAPISafety: a restart endpoint refuses without the org's name as confirm, and runs
// nothing; with --read-only every write is refused; progress streams as events.
func TestBerthAPISafety(t *testing.T) {
	ctx := context.Background()
	c, _, snap := apiSession(t, twoOrgs, mcpRules)
	for name, call := range map[string]func(string) error{
		"restart": func(confirm string) error { _, err := c.OrgRestart(ctx, "acme", confirm); return err },
		"up":      func(confirm string) error { _, err := c.OrgUp(ctx, "acme", confirm); return err },
		"down":    func(confirm string) error { _, err := c.OrgDown(ctx, "acme", confirm); return err },
	} {
		for _, confirm := range []string{"", "yes", "globex", "ACME"} {
			if err := call(confirm); kindOf(err) != ops.KindRefused || !strings.Contains(err.Error(), "stops the work running in acme") {
				t.Errorf("%s with confirm %q: %v", name, confirm, err)
			}
		}
	}
	if status, body := raw(t, c, "POST", "/v1/orgs/acme/restart", ""); status != 403 || !strings.Contains(body, `confirm set to \"acme\"`) {
		t.Errorf("POST restart with no body: %d %s", status, body)
	}
	res := snap()
	if hasCall(res, `"compose"`) {
		t.Errorf("an unconfirmed restart ran compose:\n%s", strings.Join(res.Calls, "\n"))
	}
	if got := auditLines(t, res); len(got) != 13 || !strings.Contains(got[0], "|refused|") {
		t.Errorf("the refused restarts in the audit log: %v", got)
	}
	// Confirmed, and asked for as a stream: its progress, then the result.
	var events []string
	err := c.Stream(ctx, "POST", "/v1/orgs/acme/restart", map[string]any{"confirm": "acme"}, func(event string, data json.RawMessage) {
		events = append(events, event)
		if event == "start" && !strings.Contains(string(data), `"op":"restart"`) {
			t.Errorf("the start event: %s", data)
		}
	})
	if err != nil || len(events) < 3 || events[0] != "start" || events[len(events)-2] != "done" || events[len(events)-1] != "result" {
		t.Errorf("a streamed restart: %v, events %v", err, events)
	}
	// A stream that is refused ends with the error.
	if err := c.Stream(ctx, "POST", "/v1/orgs/acme/restart", nil, nil); kindOf(err) != ops.KindRefused {
		t.Errorf("a refused stream: %v", err)
	}

	// --read-only: reads answer; no write runs, confirmed or not.
	c, _, snap = apiSession(t, twoOrgs, mcpRules, "--read-only")
	if info, err := c.Info(ctx); err != nil || !info.ReadOnly || info.Writes || info.Restarts {
		t.Errorf("--read-only, GET /v1: %+v %v", info, err)
	}
	if o, err := c.OrgsList(ctx); err != nil || len(o.Orgs) != 2 {
		t.Errorf("--read-only, orgs: %v %v", o, err)
	}
	for name, err := range map[string]error{
		"firewall_allow": func() error { _, err := c.FirewallAllow(ctx, "acme", []string{"x.example"}); return err }(),
		"repo_add":       func() error { _, err := c.RepoAdd(ctx, "acme", "acme/widget", "", "", false); return err }(),
		"backup_create":  func() error { _, err := c.BackupCreate(ctx, []string{"acme"}, false); return err }(),
		"org_restart":    func() error { _, err := c.OrgRestart(ctx, "acme", "acme"); return err }(),
		"org_down":       func() error { _, err := c.OrgDown(ctx, "acme", "acme"); return err }(),
	} {
		if kindOf(err) != ops.KindRefused {
			t.Errorf("--read-only, %s: %v", name, err)
		}
	}
	res = snap()
	if hasCall(res, `"compose"`) || hasCall(res, "archive") || treeLine(res, "state/audit.log") != "" ||
		strings.Contains(treeLine(res, "state/orgs/acme/config/firewall.txt"), "x.example") {
		t.Errorf("--read-only: something ran or was written:\n%s", strings.Join(res.Calls, "\n"))
	}
}
