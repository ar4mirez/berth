package app

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// Install is `ccenv install [bin-dir]`, plus `--alias NAME`: link berth onto PATH (default
// ~/.local/bin) and install its bash completion. --alias also links NAME to berth, with completion
// for that name: `berth install --alias ccenv` is the optional last cutover step (plan).
// completion returns the bash completion script for a command name.
func (a *App) Install(_ context.Context, args []string, completion func(name string) ([]byte, error)) error {
	bin, alias := "", ""
	for i := 0; i < len(args); i++ {
		switch x := args[i]; {
		case x == "--alias":
			if i+1 >= len(args) || args[i+1] == "" {
				return fmt.Errorf("%s needs a value", x)
			}
			i++
			alias = args[i]
			if alias == Tool || strings.ContainsAny(alias, "/ ") {
				return fmt.Errorf("invalid alias '%s'", alias)
			}
		case strings.HasPrefix(x, "-"):
			return fmt.Errorf("unknown flag %s", x)
		case bin == "":
			bin = x // ccenv's "${1:-…}": the first argument; the rest are ignored
		}
	}
	home := a.Getenv("HOME")
	if bin == "" {
		bin = home + "/.local/bin"
	}
	data := a.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = home + "/.local/share"
	}
	comp := data + "/bash-completion/completions"
	if a.Self == "" {
		return fmt.Errorf("can't tell where the %s binary is", Tool)
	}
	for _, d := range []string{bin, comp} {
		if err := a.Host.FS.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	names := []string{Tool}
	if alias != "" {
		names = append(names, alias)
	}
	for _, n := range names {
		link := path.Join(bin, n)
		if err := a.Host.FS.Symlink(a.Self, link); err != nil {
			return err
		}
		script, err := completion(n)
		if err != nil {
			return err
		}
		if err := a.Host.FS.WriteFile(path.Join(comp, n), script, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(a.Stdout, "Linked %s -> %s\n", link, a.Self)
		fmt.Fprintf(a.Stdout, "Completion installed: %s (new shells pick it up)\n", path.Join(comp, n))
	}
	if !strings.Contains(":"+a.Getenv("PATH")+":", ":"+bin+":") {
		fmt.Fprintf(a.Stdout, "NOTE: %s is not on PATH. Add: export PATH=\"%s:$PATH\"\n", bin, bin)
	}
	return nil
}
