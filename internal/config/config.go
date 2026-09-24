// Package config resolves berth's state root and holds the global --read-only guard.
//
// The state root is the directory holding orgs/ and backups/ (ccenv's $ROOT). Resolution order:
//
//	--home > $BERTH_HOME > config.yaml (home:) > ~/.local/share/berth
//
// The default is empty on purpose, so berth can't touch the live ccenv orgs by accident; at
// cutover, config.yaml points home: at the legacy checkout (docs/plan.md, decision 6).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Source says which rule picked the state root.
type Source string

const (
	SourceFlag    Source = "--home"
	SourceEnv     Source = "$BERTH_HOME"
	SourceConfig  Source = "config"
	SourceDefault Source = "default"
)

// Home is a resolved state root.
type Home struct {
	Path   string
	Source Source
	// ConfigFile is the config.yaml that was consulted, set only when Source is SourceConfig.
	ConfigFile string
}

// Inputs are the resolver's only dependencies, so tests don't touch the real environment.
type Inputs struct {
	Flag    string              // value of --home ("" when unset)
	Getenv  func(string) string // os.Getenv in production
	UserDir string              // the user's home directory (os.UserHomeDir)
}

// FromOS builds Inputs from the real process environment.
func FromOS(flag string) (Inputs, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return Inputs{}, fmt.Errorf("can't find your home directory: %w", err)
	}
	return Inputs{Flag: flag, Getenv: os.Getenv, UserDir: dir}, nil
}

// ConfigPath is berth's config file: $XDG_CONFIG_HOME/berth/config.yaml, else ~/.config/berth/config.yaml.
func ConfigPath(in Inputs) string {
	return filepath.Join(xdg(in, "XDG_CONFIG_HOME", ".config"), "berth", "config.yaml")
}

// DefaultHome is the state root when nothing else is set: $XDG_DATA_HOME/berth, else ~/.local/share/berth.
func DefaultHome(in Inputs) string {
	return filepath.Join(xdg(in, "XDG_DATA_HOME", filepath.Join(".local", "share")), "berth")
}

func xdg(in Inputs, env, fallback string) string {
	if v := in.Getenv(env); filepath.IsAbs(v) { // the XDG spec says to ignore relative values
		return v
	}
	return filepath.Join(in.UserDir, fallback)
}

// Resolve picks the state root. It never creates anything.
func Resolve(in Inputs) (Home, error) {
	if in.Flag != "" {
		p, err := clean(in, in.Flag, true)
		if err != nil {
			return Home{}, fmt.Errorf("--home: %w", err)
		}
		return Home{Path: p, Source: SourceFlag}, nil
	}
	if v := in.Getenv("BERTH_HOME"); v != "" {
		p, err := clean(in, v, true)
		if err != nil {
			return Home{}, fmt.Errorf("BERTH_HOME: %w", err)
		}
		return Home{Path: p, Source: SourceEnv}, nil
	}
	cf := ConfigPath(in)
	fc, err := readFile(cf)
	if err != nil {
		return Home{}, err
	}
	if fc.Home != "" {
		// Relative paths are refused here: relative to what? The config file's dir and the cwd both surprise.
		p, err := clean(in, fc.Home, false)
		if err != nil {
			return Home{}, fmt.Errorf("%s: home: %w", cf, err)
		}
		return Home{Path: p, Source: SourceConfig, ConfigFile: cf}, nil
	}
	return Home{Path: DefaultHome(in), Source: SourceDefault}, nil
}

// File is the schema of config.yaml. Unknown keys are an error, so a typo can't silently fall
// through to the default root.
type File struct {
	Home string `yaml:"home"`
}

func readFile(path string) (File, error) {
	var f File
	// The operator's own config, on the machine berth runs on (not a host), so not org state.
	b, err := os.ReadFile(path) //nolint:forbidigo,gosec // see above; the path is berth's own config location
	if errors.Is(err, fs.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("reading %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return f, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// clean expands a leading ~ and makes p absolute. Relative paths are resolved against the working
// directory only when allowRel is set (flags and env vars, which are typed in a shell).
func clean(in Inputs, p string, allowRel bool) (string, error) {
	switch {
	case p == "~":
		p = in.UserDir
	case strings.HasPrefix(p, "~/"):
		p = filepath.Join(in.UserDir, p[2:])
	case strings.HasPrefix(p, "~"):
		return "", fmt.Errorf("%q: only ~ and ~/ are expanded", p)
	}
	if !filepath.IsAbs(p) {
		if !allowRel {
			return "", fmt.Errorf("%q must be an absolute path (or start with ~/)", p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		p = abs
	}
	return filepath.Clean(p), nil
}
