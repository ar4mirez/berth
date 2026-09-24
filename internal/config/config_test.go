package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// env builds a Getenv over a fixed map, so tests never see the real environment.
func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// writeConfig writes config.yaml under dir/berth/ and returns its path.
func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "berth", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolvePrecedence(t *testing.T) {
	user := t.TempDir()
	cfg := writeConfig(t, filepath.Join(user, ".config"), "home: /srv/from-config\n")

	tests := []struct {
		name   string
		flag   string
		env    map[string]string
		want   string
		source Source
	}{
		{"flag beats env and config", "/srv/from-flag", map[string]string{"BERTH_HOME": "/srv/from-env"}, "/srv/from-flag", SourceFlag},
		{"env beats config", "", map[string]string{"BERTH_HOME": "/srv/from-env"}, "/srv/from-env", SourceEnv},
		{"empty env is unset", "", map[string]string{"BERTH_HOME": ""}, "/srv/from-config", SourceConfig},
		{"config beats default", "", nil, "/srv/from-config", SourceConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := Resolve(Inputs{Flag: tt.flag, Getenv: env(tt.env), UserDir: user})
			if err != nil {
				t.Fatal(err)
			}
			if h.Path != tt.want || h.Source != tt.source {
				t.Errorf("got %s (%s), want %s (%s)", h.Path, h.Source, tt.want, tt.source)
			}
			if (h.Source == SourceConfig) != (h.ConfigFile == cfg) {
				t.Errorf("ConfigFile = %q with source %s", h.ConfigFile, h.Source)
			}
		})
	}
}

func TestResolveDefault(t *testing.T) {
	user := t.TempDir()
	h, err := Resolve(Inputs{Getenv: env(nil), UserDir: user})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(user, ".local", "share", "berth"); h.Path != want || h.Source != SourceDefault {
		t.Errorf("got %s (%s), want %s (default)", h.Path, h.Source, want)
	}
	if _, err := os.Stat(h.Path); !os.IsNotExist(err) {
		t.Errorf("Resolve must not create the default root (stat err: %v)", err)
	}
}

func TestResolveXDG(t *testing.T) {
	user, xdgConfig, xdgData := t.TempDir(), t.TempDir(), t.TempDir()
	writeConfig(t, filepath.Join(user, ".config"), "home: /srv/ignored\n") // not consulted when XDG_CONFIG_HOME is set

	h, err := Resolve(Inputs{Getenv: env(map[string]string{"XDG_CONFIG_HOME": xdgConfig, "XDG_DATA_HOME": xdgData}), UserDir: user})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(xdgData, "berth"); h.Path != want {
		t.Errorf("default with XDG_DATA_HOME: got %s, want %s", h.Path, want)
	}

	writeConfig(t, xdgConfig, "home: /srv/xdg\n")
	h, err = Resolve(Inputs{Getenv: env(map[string]string{"XDG_CONFIG_HOME": xdgConfig}), UserDir: user})
	if err != nil || h.Path != "/srv/xdg" {
		t.Errorf("config under XDG_CONFIG_HOME: got %s, %v", h.Path, err)
	}

	// Relative XDG values are ignored, per the XDG spec.
	h, err = Resolve(Inputs{Getenv: env(map[string]string{"XDG_CONFIG_HOME": "rel", "XDG_DATA_HOME": "rel"}), UserDir: user})
	if err != nil || h.Path != "/srv/ignored" {
		t.Errorf("relative XDG_CONFIG_HOME: got %s, %v", h.Path, err)
	}
}

func TestResolvePaths(t *testing.T) {
	user := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, flag, envHome, want string
	}{
		{"tilde alone", "~", "", user},
		{"tilde slash", "~/state", "", filepath.Join(user, "state")},
		{"env tilde", "", "~/state", filepath.Join(user, "state")},
		{"relative flag uses cwd", "state/../x", "", filepath.Join(cwd, "x")},
		{"trailing slash cleaned", "/srv/x/", "", "/srv/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, err := Resolve(Inputs{Flag: tt.flag, Getenv: env(map[string]string{"BERTH_HOME": tt.envHome}), UserDir: user})
			if err != nil || h.Path != tt.want {
				t.Errorf("got %q, %v; want %q", h.Path, err, tt.want)
			}
		})
	}
	if _, err := Resolve(Inputs{Flag: "~other/x", Getenv: env(nil), UserDir: user}); err == nil {
		t.Error("~user must be refused, not guessed")
	}
}

func TestConfigFile(t *testing.T) {
	tests := []struct {
		name, body string
		want       string // "" = default root
		errPart    string
	}{
		{"empty file", "", "", ""},
		{"comments only", "# nothing yet\n", "", ""},
		{"empty home", "home: \"\"\n", "", ""},
		{"tilde home", "home: ~/legacy\n", "~/legacy", ""},
		{"relative home refused", "home: legacy\n", "", "must be an absolute path"},
		{"unknown key refused", "hom: /srv/typo\n", "", "field hom not found"},
		{"invalid yaml refused", "home: [\n", "", "config.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := t.TempDir()
			writeConfig(t, filepath.Join(user, ".config"), tt.body)
			h, err := Resolve(Inputs{Getenv: env(nil), UserDir: user})
			if tt.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errPart) {
					t.Fatalf("got %v, want error containing %q", err, tt.errPart)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := DefaultHome(Inputs{Getenv: env(nil), UserDir: user})
			if tt.want != "" {
				want = filepath.Join(user, strings.TrimPrefix(tt.want, "~/"))
			}
			if h.Path != want {
				t.Errorf("got %s, want %s", h.Path, want)
			}
		})
	}
}

func TestConfigFileUnreadable(t *testing.T) {
	user := t.TempDir()
	// A directory where the file should be: reading fails with something other than "not found".
	if err := os.MkdirAll(filepath.Join(user, ".config", "berth", "config.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(Inputs{Getenv: env(nil), UserDir: user}); err == nil {
		t.Fatal("an unreadable config must be an error, not a silent fall-through to the default")
	}
	// ...but a higher-precedence source doesn't need the config file at all.
	if h, err := Resolve(Inputs{Flag: "/srv/x", Getenv: env(nil), UserDir: user}); err != nil || h.Path != "/srv/x" {
		t.Errorf("--home with a broken config: %s, %v", h.Path, err)
	}
}

func TestWritable(t *testing.T) {
	if err := (State{}).Writable("write org.env"); err != nil {
		t.Errorf("writable state refused: %v", err)
	}
	err := State{ReadOnly: true}.Writable("write org.env")
	if !errors.Is(err, ErrReadOnly) || !strings.Contains(err.Error(), "refusing to write org.env") {
		t.Errorf("read-only state: %v", err)
	}
}
