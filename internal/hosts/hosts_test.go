package hosts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/ar4mirez/berth/internal/host/local"
)

func TestParseTarget(t *testing.T) {
	for in, want := range map[string][2]string{
		"box.example":              {"op", "box.example:22"},
		"deploy@box.example":       {"deploy", "box.example:22"},
		"deploy@box.example:2222":  {"deploy", "box.example:2222"},
		"192.0.2.7:2200":           {"op", "192.0.2.7:2200"},
		"[2001:db8::1]:22":         {"op", "[2001:db8::1]:22"},
		"[2001:db8::1]":            {"op", "[2001:db8::1]:22"},
		"a@b@box.example":          {"a@b", "box.example:22"},
		"ops@box.tail0.ts.net:222": {"ops", "box.tail0.ts.net:222"},
	} {
		u, a, err := ParseTarget(in, "op")
		if err != nil || u != want[0] || a != want[1] {
			t.Errorf("%s: %s %s %v, want %v", in, u, a, err, want)
		}
	}
	for _, in := range []string{"", "box:0", "box:99999", "box:x", "2001:db8::1", "a b", "@box"} {
		if u, a, err := ParseTarget(in, "op"); err == nil {
			t.Errorf("%q: accepted as %s %s", in, u, a)
		}
	}
	if _, _, err := ParseTarget("box", ""); err == nil {
		t.Error("no user anywhere must fail")
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"box1", "a", "build-arm64"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"local", "", "Box", "-x", "a_b", "a.b", strings.Repeat("a", 33)} {
		if CheckName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRegistryRoundTrip(t *testing.T) {
	fsys := local.FS{}
	p := Paths{Dir: filepath.Join(t.TempDir(), "berth")}
	r, err := Load(fsys, p)
	if err != nil || len(r.Hosts) != 0 {
		t.Fatalf("missing file: %+v %v", r, err)
	}
	r.Hosts = append(r.Hosts, Entry{Name: "box1", Kind: KindSSH, User: "ops", Addr: "box.example:22", Home: "/home/ops/.local/share/berth", Key: p.Key("box1")})
	if err := Save(fsys, p, r); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p.File()); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
	got, err := Load(fsys, p)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := got.Find("box1")
	if !ok || e != r.Hosts[0] || e.Address() != "ops@box.example:22" {
		t.Errorf("round trip: %+v", got)
	}
	if !got.Remove("box1") || got.Remove("box1") || len(got.Hosts) != 0 {
		t.Error("Remove")
	}

	for name, body := range map[string]string{
		"unknown key": "hosts:\n  - name: box1\n    kind: ssh\n    user: u\n    addr: h:22\n    home: /h\n    key: /k\n    port: 22\n",
		"twice":       "hosts:\n  - {name: a, kind: ssh, user: u, addr: 'h:22', home: /h, key: /k}\n  - {name: a, kind: ssh, user: u, addr: 'h:22', home: /h, key: /k}\n",
		"incomplete":  "hosts:\n  - {name: a, kind: ssh, user: u, home: /h, key: /k}\n",
		"relative":    "hosts:\n  - {name: a, kind: ssh, user: u, addr: 'h:22', home: h, key: /k}\n",
		"local":       "hosts:\n  - {name: local, kind: ssh, user: u, addr: 'h:22', home: /h, key: /k}\n",
	} {
		if err := os.WriteFile(p.File(), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(fsys, p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAuthorizedKeys(t *testing.T) {
	k1, err := NewKey("t")
	if err != nil {
		t.Fatal(err)
	}
	k2, _ := NewKey("t")
	if pub, err := PublicOf(k1.Private); err != nil || string(pub.Marshal()) != string(k1.Public.Marshal()) {
		t.Fatalf("PublicOf: %v", err)
	}
	theirs := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMm0lM6q2zX2k4QeDq7ow5ZlJcJ1Lq5dQq5V0cJ2o5Vd operator@laptop" // no trailing newline
	c := AddAuthorized([]byte(theirs), k1.AuthorizedLine(Comment("box1")))
	if want := theirs + "\n" + k1.AuthorizedLine("berth:box1") + "\n"; string(c) != want {
		t.Fatalf("add: %q", c)
	}
	if again := AddAuthorized(c, k1.AuthorizedLine("other")); string(again) != string(c) {
		t.Error("adding a key that's there must change nothing")
	}
	c = AddAuthorized(c, `from="192.0.2.1" `+k2.AuthorizedLine("x"))
	out, found := RemoveAuthorized(c, k2.Public)
	if !found || strings.Contains(string(out), string(gossh.MarshalAuthorizedKey(k2.Public))[:40]) {
		t.Errorf("remove with options: %v %q", found, out)
	}
	out, found = RemoveAuthorized(out, k1.Public)
	if !found || string(out) != theirs+"\n" {
		t.Errorf("remove: %q", out)
	}
	if _, found := RemoveAuthorized(out, k1.Public); found {
		t.Error("found a removed key")
	}
}
