package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/config"
	"github.com/ar4mirez/berth/internal/host/local"
	"github.com/ar4mirez/berth/internal/hosts"
)

// fakeHetzner is Hetzner Cloud's API, as far as berth uses it: firewalls and servers, with labels.
type fakeHetzner struct {
	mu        sync.Mutex
	next      int64
	servers   map[int64]map[string]any
	firewalls map[int64]map[string]any
	// failServer refuses to create servers; inUse is how many times a firewall's deletion is
	// refused as "resource_in_use" first.
	failServer bool
	inUse      int
	calls      []string
}

func (f *fakeHetzner) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	fail := func(status int, code, msg string) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
	}
	if r.Header.Get("Authorization") != "Bearer hc-test-token" {
		fail(401, "unauthorized", "unable to authenticate")
		return
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	matches := func(res map[string]any) bool {
		labels, _ := res["labels"].(map[string]any)
		for _, cond := range strings.Split(r.URL.Query().Get("label_selector"), ",") {
			if k, v, ok := strings.Cut(cond, "=="); ok && labels[k] != v {
				return false
			}
		}
		return true
	}
	list := func(key string, all map[int64]map[string]any) {
		out := []any{}
		for _, res := range all {
			if matches(res) {
				out = append(out, res)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{key: out})
	}
	var id int64
	_, _ = fmt.Sscanf(filepath.Base(r.URL.Path), "%d", &id)
	switch {
	case r.Method == "POST" && r.URL.Path == "/firewalls":
		f.next++
		body["id"] = f.next
		f.firewalls[f.next] = body
		_ = json.NewEncoder(w).Encode(map[string]any{"firewall": body})
	case r.Method == "POST" && r.URL.Path == "/servers":
		if f.failServer {
			fail(422, "resource_limit_exceeded", "server limit reached")
			return
		}
		f.next++
		body["id"], body["status"] = f.next, "initializing"
		f.servers[f.next] = body
		_ = json.NewEncoder(w).Encode(map[string]any{"server": body})
	case r.Method == "GET" && r.URL.Path == "/servers":
		list("servers", f.servers)
	case r.Method == "GET" && r.URL.Path == "/firewalls":
		list("firewalls", f.firewalls)
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/servers/"):
		delete(f.servers, id)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/firewalls/"):
		if f.inUse > 0 {
			f.inUse--
			fail(422, "resource_in_use", "firewall is still in use")
			return
		}
		delete(f.firewalls, id)
		w.WriteHeader(204)
	default:
		fail(404, "not_found", r.Method+" "+r.URL.Path)
	}
}

func (f *fakeHetzner) names() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, s := range f.servers {
		out = append(out, "server:"+s["name"].(string))
	}
	for _, s := range f.firewalls {
		out = append(out, "firewall:"+s["name"].(string))
	}
	return strings.Join(out, ",")
}

// cloudApp is an App with a fake Hetzner behind it, and a registration that records the host as
// host add would, without a machine to reach.
func cloudApp(t *testing.T, f *fakeHetzner, env map[string]string) (*App, *bytes.Buffer, *[]CloudHost) {
	t.Helper()
	f.servers, f.firewalls = map[int64]map[string]any{}, map[int64]map[string]any{}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	home := t.TempDir()
	all := map[string]string{"HOME": home, "HCLOUD_TOKEN": "hc-test-token", "BERTH_TAILSCALE_AUTHKEY": "tskey-auth-SINGLEUSE", "BERTH_HCLOUD_ENDPOINT": srv.URL}
	for k, v := range env {
		all[k] = v
	}
	var out bytes.Buffer
	a := New(config.State{Home: config.Home{Path: filepath.Join(home, "state")}}, local.New(), nil, &out, &out, func(k string) string { return all[k] })
	var registered []CloudHost
	a.CloudRegister = func(_ context.Context, c CloudHost) error {
		registered = append(registered, c)
		p := a.hostPaths()
		reg, err := hosts.Load(a.Operator.FS, p)
		if err != nil {
			return err
		}
		reg.Hosts = append(reg.Hosts, hosts.Entry{Name: c.Name, Kind: hosts.KindSSH, User: "ops", Addr: c.Addr + ":22", Home: "/home/ops/.local/share/berth", Key: p.Key(c.Name)})
		return hosts.Save(a.Operator.FS, p, reg)
	}
	return a, &out, &registered
}

// TestHostCreate: a server behind a firewall with no rules, both labelled; user data that pins the
// host key berth made and lets no password in; the host registered with that key's fingerprint.
func TestHostCreate(t *testing.T) {
	f := &fakeHetzner{}
	a, out, registered := cloudApp(t, f, nil)
	if err := a.HostCreate(context.Background(), []string{"box3", "--provider", "hetzner", "--region", "hel1"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if f.names() != "server:berth-box3,firewall:berth-box3" {
		t.Fatalf("created: %s", f.names())
	}
	var srv, fw map[string]any
	for _, s := range f.servers {
		srv = s
	}
	for _, s := range f.firewalls {
		fw = s
	}
	for _, res := range []map[string]any{srv, fw} {
		if l, _ := res["labels"].(map[string]any); l["berth.managed"] != "true" || l["berth.host"] != "box3" {
			t.Errorf("labels: %v", res["labels"])
		}
	}
	if rules, _ := fw["rules"].([]any); len(rules) != 0 {
		t.Errorf("the firewall lets something in: %v", fw["rules"])
	}
	if fws := fmt.Sprint(srv["firewalls"]); !strings.Contains(fws, fmt.Sprint(fw["id"])) || srv["location"] != "hel1" || srv["server_type"] != "cax21" || srv["image"] != "ubuntu-24.04" {
		t.Errorf("the server: firewalls %v, %v %v %v", srv["firewalls"], srv["location"], srv["server_type"], srv["image"])
	}
	ud, _ := srv["user_data"].(string)
	for _, want := range []string{"#cloud-config", "hostname: berth-box3", "ssh_pwauth: false", "disable_root: true", "ed25519_private: |", "BEGIN OPENSSH PRIVATE KEY",
		"name: ops", "lock_passwd: true", "tskey-auth-SINGLEUSE", "docker-ce=$v", "tailscale up --auth-key=file:/run/berth/tailscale-authkey --hostname=berth-box3"} {
		if !strings.Contains(ud, want) {
			t.Errorf("user data lacks %q", want)
		}
	}
	// The fingerprint berth pins is the key the server is given.
	if len(*registered) != 1 {
		t.Fatalf("registered: %v", *registered)
	}
	c := (*registered)[0]
	var pub string
	for _, l := range strings.Split(ud, "\n") {
		if strings.HasPrefix(l, "  ed25519_public: ") {
			pub = strings.TrimPrefix(l, "  ed25519_public: ")
		}
	}
	key, _, _, _, err := gossh.ParseAuthorizedKey([]byte(pub))
	if err != nil || gossh.FingerprintSHA256(key) != c.Fingerprint || c.Addr != "berth-box3" || c.Name != "box3" {
		t.Errorf("registered %+v; the user data's host key: %q (%v)", c, pub, err)
	}
	if _, err := os.Stat(c.KeyFile); err == nil {
		t.Error("the first-login key was left on disk")
	}
	if e, err := a.registered("box3"); err != nil || e.Provider != ProviderHetzner {
		t.Errorf("the registry: %+v %v", e, err)
	}
	if s := out.String(); strings.Contains(s, "hc-test-token") || strings.Contains(s, "tskey-auth") || strings.Contains(s, "PRIVATE KEY") {
		t.Errorf("a secret was printed:\n%s", s)
	}

	// Refused before anything is created.
	for name, tc := range map[string]struct {
		env  map[string]string
		args []string
		want string
	}{
		"no token":         {map[string]string{"HCLOUD_TOKEN": ""}, []string{"b", "--provider", "hetzner"}, "HCLOUD_TOKEN"},
		"no tailscale key": {map[string]string{"BERTH_TAILSCALE_AUTHKEY": ""}, []string{"b", "--provider", "hetzner"}, "BERTH_TAILSCALE_AUTHKEY"},
		"another provider": {nil, []string{"b", "--provider", "aws"}, "unknown provider"},
		"no provider":      {nil, []string{"b"}, "usage:"},
		"a bad name":       {nil, []string{"Bad_Name", "--provider", "hetzner"}, "name"},
		"a wrong token":    {map[string]string{"HCLOUD_TOKEN": "nope"}, []string{"b", "--provider", "hetzner"}, "unable to authenticate"},
	} {
		f := &fakeHetzner{}
		a, _, _ := cloudApp(t, f, tc.env)
		err := a.HostCreate(context.Background(), tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) || f.names() != "" {
			t.Errorf("%s: %v, created %q", name, err, f.names())
		}
	}
	a.State.ReadOnly = true
	if err := a.HostCreate(context.Background(), []string{"b", "--provider", "hetzner"}); err == nil || !errors.Is(err, config.ErrReadOnly) {
		t.Errorf("--read-only: %v", err)
	}
}

// TestHostCreateLeavesNothingBehind: whatever step fails, what was created is removed.
func TestHostCreateLeavesNothingBehind(t *testing.T) {
	// The server can't be created: the firewall goes.
	f := &fakeHetzner{failServer: true}
	a, out, _ := cloudApp(t, f, nil)
	err := a.HostCreate(context.Background(), []string{"box3", "--provider", "hetzner"})
	if err == nil || !strings.Contains(err.Error(), "server limit reached") || f.names() != "" {
		t.Errorf("a failed server: %v, left %q\n%s", err, f.names(), out)
	}
	if _, rerr := a.registered("box3"); rerr == nil {
		t.Error("a host was registered")
	}
	// The host never answers: the server and the firewall go, the firewall once it is free.
	f = &fakeHetzner{inUse: 1}
	a, out, _ = cloudApp(t, f, nil)
	a.CloudRegister = func(context.Context, CloudHost) error { return errors.New("berth-box3:22 didn't answer") }
	err = a.HostCreate(context.Background(), []string{"box3", "--provider", "hetzner"})
	if err == nil || !strings.Contains(err.Error(), "never became a berth host") || f.names() != "" {
		t.Errorf("a host that never answers: %v, left %q\n%s", err, f.names(), out)
	}
	if !strings.Contains(out.String(), "removed server berth-box3, firewall berth-box3") {
		t.Errorf("it doesn't say what it removed:\n%s", out)
	}
	// Something labelled for that name is already there: nothing is created over it.
	f = &fakeHetzner{}
	a, _, _ = cloudApp(t, f, nil)
	f.servers[9] = map[string]any{"id": 9, "name": "berth-box3", "labels": map[string]any{"berth.managed": "true", "berth.host": "box3"}}
	if err := a.HostCreate(context.Background(), []string{"box3", "--provider", "hetzner"}); err == nil || !strings.Contains(err.Error(), "host reconcile") || len(f.servers) != 1 || len(f.firewalls) != 0 {
		t.Errorf("a leftover server: %v, %s", err, f.names())
	}
}

// TestHostReconcileAndDestroy: reconcile finds what carries berth's labels and belongs to no
// registered host, and --prune deletes only that; destroy forgets the host and deletes its server
// and firewall, and refuses a host berth didn't create.
func TestHostReconcileAndDestroy(t *testing.T) {
	ctx := context.Background()
	f := &fakeHetzner{}
	a, out, _ := cloudApp(t, f, nil)
	if err := a.HostReconcile(ctx, nil); err != nil || !strings.Contains(out.String(), "Nothing at Hetzner carries berth's labels") {
		t.Errorf("an empty project: %v %s", err, out)
	}
	if err := a.HostCreate(ctx, []string{"box3", "--provider", "hetzner"}); err != nil {
		t.Fatal(err)
	}
	// Left behind by something that went wrong, and one that isn't berth's at all.
	lbl := func(h string) map[string]any { return map[string]any{"berth.managed": "true", "berth.host": h} }
	f.servers[90] = map[string]any{"id": 90, "name": "berth-ghost", "labels": lbl("ghost")}
	f.firewalls[91] = map[string]any{"id": 91, "name": "berth-ghost", "labels": lbl("ghost")}
	f.servers[92] = map[string]any{"id": 92, "name": "someone-elses", "labels": map[string]any{}}
	out.Reset()
	err := a.HostReconcile(ctx, nil)
	var exit *Exit
	if !errors.As(err, &exit) || exit.Code != 3 || !strings.Contains(out.String(), "ghost") || !strings.Contains(out.String(), "NOT a registered host") ||
		!strings.Contains(out.String(), "host reconcile --prune") || strings.Contains(out.String(), "someone-elses") || len(f.servers) != 3 {
		t.Errorf("reconcile: %v\n%s", err, out)
	}
	f.inUse = 2
	out.Reset()
	if err := a.HostReconcile(ctx, []string{"--prune"}); err != nil || !strings.Contains(out.String(), "Deleted ghost: server berth-ghost, firewall berth-ghost") {
		t.Errorf("reconcile --prune: %v\n%s", err, out)
	}
	if got := f.names(); !strings.Contains(got, "server:berth-box3") || !strings.Contains(got, "firewall:berth-box3") || !strings.Contains(got, "someone-elses") || strings.Contains(got, "ghost") {
		t.Errorf("after --prune: %s", got)
	}
	out.Reset()
	if err := a.HostReconcile(ctx, nil); err != nil || !strings.Contains(out.String(), "belongs to a registered host") {
		t.Errorf("reconcile, clean: %v\n%s", err, out)
	}
	a.State.ReadOnly = true
	if err := a.HostReconcile(ctx, []string{"--prune"}); !errors.Is(err, config.ErrReadOnly) {
		t.Errorf("--prune under --read-only: %v", err)
	}
	if err := a.HostDestroy(ctx, []string{"box3", "--force"}); !errors.Is(err, config.ErrReadOnly) {
		t.Errorf("destroy under --read-only: %v", err)
	}
	a.State.ReadOnly = false

	// destroy: the host can't be reached here, so berth can't see that it has no orgs: refused,
	// and nothing is deleted. --force goes ahead.
	if err := a.HostDestroy(ctx, []string{"box3"}); err == nil || !strings.Contains(err.Error(), "--force") || !strings.Contains(f.names(), "server:berth-box3") {
		t.Errorf("destroy of a host that can't be checked: %v, %s", err, f.names())
	}
	out.Reset()
	if err := a.HostDestroy(ctx, []string{"box3", "--force"}); err != nil || !strings.Contains(out.String(), "Deleted at Hetzner: server berth-box3, firewall berth-box3") {
		t.Errorf("destroy --force: %v\n%s", err, out)
	}
	if got := f.names(); got != "server:someone-elses" {
		t.Errorf("after destroy: %s", got)
	}
	if _, err := a.registered("box3"); err == nil {
		t.Error("the host is still registered")
	}
	// A host that was added, not created, isn't berth's to delete.
	p := a.hostPaths()
	reg, _ := hosts.Load(a.Operator.FS, p)
	reg.Hosts = append(reg.Hosts, hosts.Entry{Name: "mine", Kind: hosts.KindSSH, User: "ops", Addr: "mine.example:22", Home: "/h", Key: p.Key("mine")})
	if err := hosts.Save(a.Operator.FS, p, reg); err != nil {
		t.Fatal(err)
	}
	if err := a.HostDestroy(ctx, []string{"mine", "--force"}); err == nil || !strings.Contains(err.Error(), "wasn't created by berth") {
		t.Errorf("destroy of an added host: %v", err)
	}
	if err := a.HostDestroy(ctx, []string{"nope"}); err == nil || !strings.Contains(err.Error(), "unknown host") {
		t.Errorf("destroy of an unknown host: %v", err)
	}
}
