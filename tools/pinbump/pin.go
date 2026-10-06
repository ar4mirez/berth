package main

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Pin is one `# pin: datasource=… depName=…` block in image/Dockerfile: the comment, then the ARG
// lines right below it (the version first, then its checksums).
type Pin struct {
	Datasource, Dep string
	Args            []Arg
}

// Arg is one `ARG NAME=value` line of a pin block.
type Arg struct{ Name, Value string }

// Value returns the value of the pin's ARG name.
func (p Pin) Value(name string) string {
	for _, a := range p.Args {
		if a.Name == name {
			return a.Value
		}
	}
	return ""
}

// ParsePins reads every pin block of a Dockerfile.
func ParsePins(dockerfile []byte) ([]Pin, error) {
	var pins []Pin
	var cur *Pin
	sc := bufio.NewScanner(bytes.NewReader(dockerfile))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "# pin:"); ok {
			p := Pin{}
			for _, f := range strings.Fields(rest) {
				k, v, _ := strings.Cut(f, "=")
				switch k {
				case "datasource":
					p.Datasource = v
				case "depName":
					p.Dep = v
				}
			}
			if p.Datasource == "" || p.Dep == "" {
				return nil, fmt.Errorf("line %d: a pin needs datasource= and depName=", n)
			}
			pins = append(pins, p)
			cur = &pins[len(pins)-1]
			continue
		}
		if rest, ok := strings.CutPrefix(line, "ARG "); ok && cur != nil {
			name, value, ok := strings.Cut(rest, "=")
			if !ok {
				return nil, fmt.Errorf("line %d: ARG %s has no pinned value", n, name)
			}
			cur.Args = append(cur.Args, Arg{name, value})
			continue
		}
		cur = nil
	}
	return pins, sc.Err()
}

// Apply rewrites the ARG lines named in set, and nothing else. Every name must be there exactly once.
func Apply(dockerfile []byte, set map[string]string) ([]byte, error) {
	lines := strings.SplitAfter(string(dockerfile), "\n")
	seen := map[string]int{}
	for i, l := range lines {
		rest, ok := strings.CutPrefix(l, "ARG ")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(rest, "=")
		if v, ok := set[name]; ok {
			seen[name]++
			nl := ""
			if strings.HasSuffix(l, "\n") {
				nl = "\n"
			}
			lines[i] = "ARG " + name + "=" + v + nl
		}
	}
	for name := range set {
		if seen[name] != 1 {
			return nil, fmt.Errorf("ARG %s: found %d times, want once", name, seen[name])
		}
	}
	return []byte(strings.Join(lines, "")), nil
}

// Newer reports whether version a is newer than b: numeric dot-separated parts, with or without a
// leading v (gh's v2.101.0, mise's v2026.9.14, ttyd's 1.7.7, Claude Code's 2.1.281). A part that
// isn't a number compares as text.
func Newer(a, b string) bool {
	pa := strings.FieldsFunc(strings.TrimPrefix(a, "v"), func(r rune) bool { return r == '.' || r == '-' })
	pb := strings.FieldsFunc(strings.TrimPrefix(b, "v"), func(r rune) bool { return r == '.' || r == '-' })
	for i := 0; i < len(pa) || i < len(pb); i++ {
		if i >= len(pa) {
			return false
		}
		if i >= len(pb) {
			return true
		}
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		switch {
		case ea == nil && eb == nil && na != nb:
			return na > nb
		case (ea != nil || eb != nil) && pa[i] != pb[i]:
			return pa[i] > pb[i]
		}
	}
	return false
}
