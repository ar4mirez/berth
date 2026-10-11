package parity

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ar4mirez/berth/internal/mcpsrv"
	"github.com/ar4mirez/berth/internal/ops"
)

// mcpSession runs `berth mcp` (with flags) against the parity fixtures and connects an MCP client
// to it over stdio: the real binary, the real protocol, and the fake docker behind it.
func mcpSession(t *testing.T, files map[string]File, rules []Rule, flags ...string) (*mcp.ClientSession, func() Result) {
	t.Helper()
	sess, err := env.Start(t.TempDir(), allCalls(), Scenario{Args: append([]string{"mcp"}, flags...), Files: files, Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "berth-conformance", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: sess.Cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, func() Result {
		t.Helper()
		r, err := sess.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
}

// callTool calls a tool and returns its structured result as JSON, and its text (the error's, when
// it is one).
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (structured string, text string, isError bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: a protocol error, not a tool result: %v", name, err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		structured = string(b)
	}
	return structured, text, res.IsError
}

// mcpRules is acme running, with what the read tools ask of it.
var mcpRules = append([]Rule{
	{Bin: "docker", Match: `^exec -u node claude-acme sh -c env -u CLAUDE_CODE_OAUTH_TOKEN claude auth status`, Stdout: authStatusOcto},
	{Bin: "docker", Match: `^exec -u node claude-acme sh -c gh api user`, Stdout: "octo-acme\n"},
	{Bin: "docker", Match: `init-firewall\.sh presets$`, Stdout: "@python     pypi.org files.pythonhosted.org\n"},
	{Bin: "docker", Match: `^logs --tail 2 claude-acme$`, Stdout: "firewall: ON (12 allowlisted networks)\nclaude-env[acme]: ready\n"},
	{Bin: "systemctl", Match: `show-environment`, Exit: 1},
	{Bin: "crontab", Match: `^-l$`, Exit: 1},
	{Bin: "docker", Match: `^version`, Stdout: "29.0.0\n"},
}, acmeRunning...)

// TestBerthMCPConformance: an MCP client sees every tool with a description, an object input
// schema and annotations that say what the catalog says of its operation; and every reading tool
// answers with the document its command prints (#61).
func TestBerthMCPConformance(t *testing.T) {
	files := merge(jsonRepos, map[string]File{
		"state/backups": {Dir: true},
		"state/backups/acme-20261006-023000.tar.zst.age": {Content: "backup", Mode: 0o600},
		"state/backups/globex-20261005-023000.tar.zst":   {Content: "b", Mode: 0o600},
		"state/backups/notes.txt":                        {Content: "x"},
	})
	cs, _ := mcpSession(t, files, mcpRules)
	ctx := context.Background()

	// The server, as it introduces itself.
	init := cs.InitializeResult()
	if init.ServerInfo.Name != "berth" || init.Capabilities.Tools == nil || init.Capabilities.Resources == nil ||
		!strings.Contains(init.Instructions, "read-only") || !strings.Contains(init.Instructions, "Secret values are never returned") {
		t.Errorf("initialize: %+v, instructions %q", init.ServerInfo, init.Instructions)
	}

	// Every tool, against the catalog.
	_, specs := mcpsrv.New(mcpsrv.Options{})
	byName := map[string]mcpsrv.Spec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	offered := 0
	for _, s := range specs {
		if !s.NoMCP {
			offered++
		}
	}
	if len(list.Tools) != offered || offered < 20 || offered == len(specs) {
		t.Fatalf("%d tools listed, %d of the %d registered are for MCP", len(list.Tools), offered, len(specs))
	}
	for _, tool := range list.Tools {
		spec, ok := byName[tool.Name]
		if !ok {
			t.Errorf("%s: listed, but not in the server's specs", tool.Name)
			continue
		}
		if spec.Cmd != "" {
			if op, ok := ops.Lookup(spec.Cmd, spec.Sub); !ok || op.Access != spec.Op.Access || op.Restart != spec.Op.Restart {
				t.Errorf("%s: its operation %s %s isn't what the catalog has", tool.Name, spec.Cmd, spec.Sub)
			}
		}
		in, _ := json.Marshal(tool.InputSchema)
		out, _ := json.Marshal(tool.OutputSchema)
		a := tool.Annotations
		switch {
		case len(tool.Description) < 30:
			t.Errorf("%s: no real description: %q", tool.Name, tool.Description)
		case !strings.Contains(string(in), `"type":"object"`) || !strings.Contains(string(out), `"type":"object"`):
			t.Errorf("%s: input %s, output %s", tool.Name, in, out)
		case a == nil || a.ReadOnlyHint != (spec.Op.Access == ops.Read) || a.OpenWorldHint == nil || *a.OpenWorldHint:
			t.Errorf("%s: annotations %+v for access %v", tool.Name, a, spec.Op.Access)
		case spec.Op.Restart != ops.Never && (a.DestructiveHint == nil || !*a.DestructiveHint || !strings.Contains(string(in), `"confirm"`) ||
			!strings.Contains(tool.Description, "--allow-restarts")):
			t.Errorf("%s restarts a container: it must be destructive, take confirm and say which flag it needs: %+v %s", tool.Name, a, in)
		case spec.Op.Access == ops.Write && spec.Op.Restart == ops.Never && !strings.Contains(tool.Description, "--allow-writes"):
			t.Errorf("%s writes: its description must say which flag it needs", tool.Name)
		case strings.Contains(string(in), `"required":["org"`) && !strings.Contains(string(in), "acme@box1"):
			t.Errorf("%s: its org argument isn't described: %s", tool.Name, in)
		}
	}

	// Every reading tool answers, with its command's document.
	reads := map[string]struct {
		args map[string]any
		want []string
	}{
		"hosts_list":       {nil, []string{`"schema":"berth.hosts/v1"`, `"name":"local"`}},
		"orgs_list":        {nil, []string{`"schema":"berth.orgs/v1"`, `"name":"acme"`, `"remote":"on"`, `"name":"globex"`}},
		"org_info":         {map[string]any{"org": "acme"}, []string{`"schema":"berth.info/v1"`, `"state":"running"`, `"ssh_port":2201`}},
		"org_accounts":     {map[string]any{"orgs": []string{"acme"}}, []string{`"schema":"berth.whoami/v1"`, `"github":"octo-acme"`, `"email":"dev@example.com"`}},
		"remote_status":    {map[string]any{"org": "acme"}, []string{`"schema":"berth.remote/v1"`, `"state":"on"`}},
		"org_logs":         {map[string]any{"org": "acme", "lines": 2}, []string{`"org":"acme"`, `"claude-env[acme]: ready"`}},
		"firewall_show":    {map[string]any{"org": "acme"}, []string{`"schema":"berth.firewall/v1"`, `"live":"on (12 entries)"`}},
		"firewall_presets": {map[string]any{"org": "acme"}, []string{`"schema":"berth.firewall-presets/v1"`, `"name":"@python"`}},
		"firewall_test":    {map[string]any{"org": "acme", "hosts": []string{"pypi.org"}}, []string{`"url":"https://pypi.org"`, `"allowed":true`}},
		"repos_list":       {map[string]any{"org": "acme"}, []string{`"schema":"berth.repos/v1"`, `"dir":"app"`, `"unregistered":[`}},
		"env_list":         {map[string]any{"org": "acme"}, []string{`"schema":"berth.env/v1"`, `"vars":[]`}},
		"packages_list":    {map[string]any{"org": "acme"}, []string{`"schema":"berth.packages/v1"`, `"packages":[]`}},
		"schedule_status":  {nil, []string{`"schema":"berth.schedule/v1"`, `"kind":"none"`}},
		"backups_list":     {map[string]any{"org": "acme"}, []string{`"file":"acme-20261006-023000.tar.zst.age"`, `"encryption":"age"`, `"stamp":"20261006-023000"`}},
		"package_presets":  {nil, []string{`"schema":"berth.package-presets/v1"`, `"name":"@playwright-chromium"`}},
	}
	for _, s := range specs {
		if s.Op.Access != ops.Read {
			continue
		}
		c, ok := reads[s.Name]
		if !ok {
			t.Errorf("%s reads, and this test doesn't call it", s.Name)
			continue
		}
		structured, text, isErr := callTool(t, cs, s.Name, c.args)
		if isErr {
			t.Errorf("%s: %s", s.Name, text)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(structured, w) {
				t.Errorf("%s: no %s in %s", s.Name, w, structured)
			}
		}
		// Secrets never come back: not the token in org.env, not a password.
		if strings.Contains(structured+text, "sk-ant-") || strings.Contains(structured+text, "PARITY-TOKEN") {
			t.Errorf("%s returned a secret: %s", s.Name, structured)
		}
	}
	if s, _, _ := callTool(t, cs, "backups_list", nil); strings.Contains(s, "notes.txt") || !strings.Contains(s, "globex-20261005") {
		t.Errorf("backups_list, every org: %s", s)
	}

	// Errors are tool errors with the command line's message and hint, not protocol failures.
	for name, c := range map[string]struct {
		args map[string]any
		want string
	}{
		"org_info":      {map[string]any{"org": "nope"}, "unknown org 'nope' (run: <tool> init nope)"},
		"remote_status": {map[string]any{"org": "globex"}, "claude-globex is not running (<tool> up globex)"},
		"org_logs":      {map[string]any{"org": ""}, "org is required"},
		"firewall_test": {map[string]any{"org": "acme", "hosts": []string{"--upload-file=/etc/passwd"}}, "can't start with '-'"},
	} {
		if _, text, isErr := callTool(t, cs, name, c.args); !isErr || !strings.Contains(strings.ReplaceAll(text, "berth ", "<tool> "), c.want) {
			t.Errorf("%s %v: isError %v, %q (want %q)", name, c.args, isErr, text, c.want)
		}
	}
	// An argument the schema doesn't have, or of the wrong type, never reaches the operation.
	for _, args := range []map[string]any{{"org": "acme", "lines": "many"}, {"org": "acme", "follow": true}, {}} {
		if res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "org_logs", Arguments: args}); err == nil && !res.IsError {
			t.Errorf("org_logs took %v", args)
		}
	}

	// Resources: the orgs, one org, and the docs.
	res, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := map[string]bool{}
	for _, r := range res.Resources {
		uris[r.URI] = true
	}
	for _, want := range []string{"berth://orgs", "berth://docs/getting-started", "berth://docs/guides/firewall", "berth://docs/PARITY"} {
		if !uris[want] {
			t.Errorf("no resource %s", want)
		}
	}
	for uri, want := range map[string]string{
		"berth://orgs":                 `"schema": "berth.orgs/v1"`,
		"berth://orgs/acme":            `"schema": "berth.info/v1"`,
		"berth://docs/guides/firewall": "# The firewall",
		"berth://docs/PARITY":          "# Parity: legacy `ccenv`",
	} {
		rr, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(rr.Contents) != 1 || !strings.Contains(rr.Contents[0].Text, want) {
			t.Errorf("%s: %v", uri, err)
		}
	}
	for _, uri := range []string{"berth://orgs/nope", "berth://docs/../../go.mod", "berth://docs/prd/061-mcp"} {
		if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("%s was served", uri)
		}
	}
}

// auditLines are the audit log's entries, as tool|outcome|args.
func auditLines(t *testing.T, r Result) []string {
	t.Helper()
	line := treeLine(r, "state/audit.log")
	if line == "" {
		return nil
	}
	// A tree line is: path, mode, the content as a Go string.
	if !strings.HasPrefix(line, "state/audit.log 0600 ") {
		t.Errorf("the audit log isn't 0600: %.40s", line)
	}
	_, quoted, _ := strings.Cut(line, " 0600 ")
	content, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("audit log %q: %v", line, err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		var e struct {
			Via, Tool, Outcome, Time string
			Args                     map[string]any
		}
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("audit line %q: %v", l, err)
		}
		args, _ := json.Marshal(e.Args)
		if (e.Via != "mcp" && e.Via != "api" && e.Via != "mcp-http") || e.Time == "" {
			t.Errorf("audit entry: %+v", e)
		}
		out = append(out, e.Tool+"|"+e.Outcome+"|"+string(args))
		auditVia = e.Via
	}
	return out
}

func hasCall(r Result, sub string) bool {
	for _, c := range r.Calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// TestBerthMCPSafety: the server is read-only by default; writes need --allow-writes; a tool that
// restarts a container needs --allow-restarts and the org's name as confirmation; and every write
// asked for is in the audit log, refused ones too (#61).
func TestBerthMCPSafety(t *testing.T) {
	writes := map[string]map[string]any{
		"firewall_allow":  {"org": "acme", "entries": []string{"files.example.com"}},
		"firewall_deny":   {"org": "acme", "entries": []string{"pypi.org"}},
		"repo_add":        {"org": "acme", "repo": "acme/widget"},
		"repo_remove":     {"org": "acme", "dir": "app"},
		"backup_create":   {"orgs": []string{"acme"}},
		"repo_sync":       {"org": "acme"},
		"firewall_on":     {"org": "acme"},
		"firewall_off":    {"org": "acme"},
		"firewall_reload": {"org": "acme"},
		"packages_add":    {"org": "acme", "entries": []string{"jq"}, "no_build": true},
		"packages_remove": {"org": "acme", "entries": []string{"jq"}, "no_build": true},
		"remote_restart":  {"org": "acme"},
		"org_create":      {"org": "t-new", "name": "Ada Lovelace", "email": "ada@example.com"},
	}
	restarts := map[string]map[string]any{
		"org_up":      {"org": "acme", "confirm": "acme"},
		"org_restart": {"org": "acme", "confirm": "acme"},
		"org_down":    {"org": "acme", "confirm": "acme"},
	}
	// Every tool that isn't a read is in one of the two maps: a new one can't skip this test.
	_, specs := mcpsrv.New(mcpsrv.Options{})
	for _, s := range specs {
		if s.NoMCP {
			continue // no MCP server offers it: the API's scopes are its test (TestBerthAPIOverTCP)
		}
		_, w := writes[s.Name]
		_, r := restarts[s.Name]
		switch {
		case s.Op.Access == ops.Read && (w || r):
			t.Errorf("%s reads, and is tested as a write", s.Name)
		case s.Op.Access == ops.Write && s.Op.Restart == ops.Never && !w:
			t.Errorf("%s writes, and this test doesn't call it", s.Name)
		case s.Op.Restart != ops.Never && !r:
			t.Errorf("%s restarts a container, and this test doesn't call it", s.Name)
		}
	}
	files := merge(jsonRepos, map[string]File{"home/.config/berth/backup.key": {Content: "# public key: age1parity\nAGE-SECRET-KEY-1PARITY\n", Mode: 0o600}})

	// Read-only (the default): every write and restart is refused, and nothing is run or changed.
	cs, snap := mcpSession(t, files, mcpRules)
	for name, args := range merge2(writes, restarts) {
		_, text, isErr := callTool(t, cs, name, args)
		if !isErr || !strings.Contains(text, "refused") || (!strings.Contains(text, "--allow-writes") && !strings.Contains(text, "--allow-restarts")) {
			t.Errorf("read-only, %s: isError %v, %q", name, isErr, text)
		}
	}
	r := snap()
	if len(r.Calls) != 0 {
		t.Errorf("read-only: commands were run:\n%s", strings.Join(r.Calls, "\n"))
	}
	if strings.Contains(treeLine(r, "state/orgs/acme/config/firewall.txt"), "files.example.com") {
		t.Error("read-only: the firewall changed")
	}
	if got := auditLines(t, r); len(got) != len(writes)+len(restarts) || !strings.Contains(strings.Join(got, "\n"), `firewall_allow|refused|{"entries":["files.example.com"],"org":"acme"}`) {
		t.Errorf("read-only: the audit log: %v", got)
	}

	// --allow-writes: writes run and are logged; restarts are still refused.
	cs, snap = mcpSession(t, files, append([]Rule{
		{Bin: "docker", Match: `^exec claude-acme test -e /workspace/widget$`, Exit: 1},
		{Bin: "docker", Match: `^image inspect`, Exit: 0},
	}, mcpRules...), "--allow-writes")
	if s, text, isErr := callTool(t, cs, "firewall_allow", writes["firewall_allow"]); isErr || !strings.Contains(s, `allowed: files.example.com`) || !strings.Contains(s, `"files.example.com"`) {
		t.Errorf("firewall_allow: isError %v, %s %s", isErr, s, text)
	}
	if s, text, isErr := callTool(t, cs, "firewall_deny", writes["firewall_deny"]); isErr || !strings.Contains(s, "removed: pypi.org") {
		t.Errorf("firewall_deny: isError %v, %s %s", isErr, s, text)
	}
	if s, text, isErr := callTool(t, cs, "repo_add", writes["repo_add"]); isErr || !strings.Contains(s, "Registered github.com/acme/widget as /workspace/widget") {
		t.Errorf("repo_add: isError %v, %s %s", isErr, s, text)
	}
	if s, text, isErr := callTool(t, cs, "repo_remove", writes["repo_remove"]); isErr || !strings.Contains(s, "Unregistered /workspace/app") {
		t.Errorf("repo_remove: isError %v, %s %s", isErr, s, text)
	}
	if s, text, isErr := callTool(t, cs, "backup_create", writes["backup_create"]); isErr || !strings.Contains(s, "Wrote ") || !strings.Contains(s, `"encryption":"age"`) {
		t.Errorf("backup_create: isError %v, %s %s", isErr, s, text)
	}
	// What a command line would take as an option is refused before it gets there.
	for name, args := range map[string]map[string]any{
		"repo_add":       {"org": "acme", "repo": "--no-clone"},
		"repo_remove":    {"org": "acme", "dir": "--delete"},
		"firewall_allow": {"org": "acme", "entries": []string{"web-git-*.example.app"}},
		"backup_create":  {"orgs": []string{"--no-encrypt"}},
	} {
		if _, text, isErr := callTool(t, cs, name, args); !isErr {
			t.Errorf("%s %v was accepted: %s", name, args, text)
		}
	}
	for name, args := range restarts {
		if _, text, isErr := callTool(t, cs, name, args); !isErr || !strings.Contains(text, "--allow-restarts") {
			t.Errorf("--allow-writes, %s: isError %v, %q", name, isErr, text)
		}
	}
	r = snap()
	fw := treeLine(r, "state/orgs/acme/config/firewall.txt")
	if !strings.Contains(fw, "files.example.com") || strings.Contains(fw, `\npypi.org\n`) || hasCall(r, `"compose"`) {
		t.Errorf("--allow-writes: firewall.txt %s\n%s", fw, strings.Join(r.Calls, "\n"))
	}
	audit := strings.Join(auditLines(t, r), "\n")
	for _, want := range []string{`firewall_allow|ok|{"entries":["files.example.com"],"org":"acme"}`, `firewall_deny|ok|`, `repo_add|ok|`, `repo_remove|ok|{"dir":"app","org":"acme"}`,
		`repo_add|failed|`, `org_restart|refused|{"org":"acme"}`} {
		if !strings.Contains(audit, want) {
			t.Errorf("--allow-writes: the audit log lacks %s:\n%s", want, audit)
		}
	}

	// --allow-restarts: a restart needs the org's name as confirmation, then runs.
	cs, snap = mcpSession(t, files, mcpRules, "--allow-restarts")
	for name, args := range map[string]map[string]any{
		"org_restart": {"org": "acme"},
		"org_down":    {"org": "acme", "confirm": "yes"},
		"org_up":      {"org": "acme", "confirm": "globex"},
	} {
		if _, text, isErr := callTool(t, cs, name, args); !isErr || !strings.Contains(text, `confirm set to "acme"`) || !strings.Contains(text, "stops the work running in acme") {
			t.Errorf("%s without the confirmation: isError %v, %q", name, isErr, text)
		}
	}
	if r = snap(); hasCall(r, `"compose"`) {
		t.Errorf("an unconfirmed restart ran compose:\n%s", strings.Join(r.Calls, "\n"))
	}
	if _, text, isErr := callTool(t, cs, "org_restart", restarts["org_restart"]); isErr {
		t.Errorf("org_restart, confirmed: %s", text)
	}
	if _, text, isErr := callTool(t, cs, "org_down", restarts["org_down"]); isErr {
		t.Errorf("org_down, confirmed: %s", text)
	}
	// --allow-restarts implies --allow-writes.
	if _, text, isErr := callTool(t, cs, "firewall_allow", writes["firewall_allow"]); isErr {
		t.Errorf("--allow-restarts, firewall_allow: %s", text)
	}
	r = snap()
	if !hasCall(r, `"up" "-d" "--force-recreate"`) || !hasCall(r, `"down"`) {
		t.Errorf("--allow-restarts: no compose up and down:\n%s", strings.Join(r.Calls, "\n"))
	}
	audit = strings.Join(auditLines(t, r), "\n")
	if strings.Count(audit, "|refused|") != 3 || !strings.Contains(audit, `org_restart|ok|{"org":"acme"}`) || !strings.Contains(audit, `org_down|ok|`) {
		t.Errorf("--allow-restarts: the audit log:\n%s", audit)
	}

	// --read-only and a flag that allows writes contradict each other: the server doesn't start.
	out := run(t, Berth(berthBin), Scenario{Args: []string{"--read-only", "mcp", "--allow-writes"}, Files: twoOrgs})
	if out.Exit != 1 || !strings.Contains(out.Stderr, "read-only") {
		t.Errorf("--read-only mcp --allow-writes: exit %d, stderr %q", out.Exit, out.Stderr)
	}
}

func merge2(ms ...map[string]map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// TestBerthMCPBackup: backup_create runs with a backup key, and refuses, without a terminal to ask
// for a passphrase on, when there is none.
func TestBerthMCPBackup(t *testing.T) {
	cs, snap := mcpSession(t, twoOrgs, mcpRules, "--allow-writes")
	if _, text, isErr := callTool(t, cs, "backup_create", map[string]any{"orgs": []string{"acme"}}); !isErr || !strings.Contains(text, "there is no backup key") || !strings.Contains(text, "keygen") {
		t.Errorf("no key: isError %v, %q", isErr, text)
	}
	if r := snap(); hasCall(r, "archive") || hasCall(r, `"run"`) {
		t.Errorf("no key: something ran:\n%s", strings.Join(r.Calls, "\n"))
	}
	if _, text, isErr := callTool(t, cs, "backup_create", map[string]any{}); !isErr || (!strings.Contains(text, "no backup key") && !strings.Contains(text, "name the orgs")) {
		t.Errorf("no orgs: isError %v, %q", isErr, text)
	}
}

// auditVia is the interface the last audit entry read came through.
var auditVia string
