package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ar4mirez/berth/internal/ops"
)

// Colors that read on a light and on a dark terminal.
var (
	accent = lipgloss.AdaptiveColor{Light: "25", Dark: "117"}
	good   = lipgloss.AdaptiveColor{Light: "28", Dark: "114"}
	bad    = lipgloss.AdaptiveColor{Light: "160", Dark: "210"}
	faint  = lipgloss.AdaptiveColor{Light: "244", Dark: "245"}

	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	headStyle   = lipgloss.NewStyle().Bold(true)
	faintStyle  = lipgloss.NewStyle().Foreground(faint)
	goodStyle   = lipgloss.NewStyle().Foreground(good)
	badStyle    = lipgloss.NewStyle().Foreground(bad)
	cursorStyle = lipgloss.NewStyle().Reverse(true)
	tabOn       = lipgloss.NewStyle().Bold(true).Underline(true).Foreground(accent)
	boxStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)
)

// The smallest terminal the dashboard draws in.
const minW, minH = 40, 10

func (m *Model) View() string {
	if m.w < minW || m.h < minH {
		return fmt.Sprintf("berth tui needs %d×%d or more (this is %d×%d)", minW, minH, m.w, m.h)
	}
	head := titleStyle.Render("berth")
	if m.open != "" {
		head += "  " + headStyle.Render(m.open)
	}
	if m.b.ReadOnly() {
		head += "  " + faintStyle.Render("read-only")
	}
	if m.busy != "" {
		head += "  " + faintStyle.Render("running: "+m.busy+" …")
	}
	var body, keys string
	switch {
	case m.over != none:
		body, keys = m.overlayView()
	case m.open == "":
		body, keys = m.listView(), "↑↓ select · enter open · u/d/r up/down/restart · A attach · S shell · ? help · q quit"
	default:
		body, keys = m.detailView()
	}
	lines := strings.Split(body, "\n")
	room := m.h - 3 // the title, a blank line, the keys
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	out := append([]string{head, ""}, lines...)
	out = append(out, faintStyle.Render(keys))
	clip := lipgloss.NewStyle().MaxWidth(m.w)
	for i, l := range out {
		out[i] = clip.Render(l)
	}
	return strings.Join(out, "\n")
}

// table lays rows out in columns, the first row as the header; cur marks a row (-1 for none).
func table(rows [][]string, cur int) string {
	if len(rows) == 0 {
		return ""
	}
	width := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			width[i] = max(width[i], lipgloss.Width(c))
		}
	}
	var b strings.Builder
	for n, r := range rows {
		mark := "  "
		if n > 0 && n-1 == cur {
			mark = "▸ "
		}
		var line strings.Builder
		for i, c := range r {
			line.WriteString(c + strings.Repeat(" ", width[i]-lipgloss.Width(c)+2))
		}
		text := strings.TrimRight(line.String(), " ")
		switch {
		case n == 0:
			text = headStyle.Render(text)
		case n > 0 && n-1 == cur:
			text = cursorStyle.Render(text)
		}
		b.WriteString(mark + text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func port(p *int) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprint(*p)
}

func yes(b bool, y, n string) string {
	if b {
		return y
	}
	return n
}

func (m *Model) listView() string {
	if !m.loaded {
		return faintStyle.Render("reading orgs…")
	}
	var b strings.Builder
	if len(m.orgs.Orgs) == 0 {
		b.WriteString(faintStyle.Render(ops.Respell("No orgs yet. Create one: berth init <org>")) + "\n")
	} else {
		rows := [][]string{{"ORG", "HOST", "STATE", "SSH", "TTYD", "TOKEN", "REMOTE"}}
		for _, o := range m.orgs.Orgs {
			host := o.Host
			if host == "" {
				host = "local"
			}
			rows = append(rows, []string{o.Name, host, o.State, port(o.SSHPort), port(o.TTYDPort), yes(o.Token, "set", "-"), o.Remote})
		}
		b.WriteString(table(rows, m.cur) + "\n")
	}
	for _, u := range m.orgs.Unreachable {
		b.WriteString(badStyle.Render(fmt.Sprintf("  %s is unreachable: %s", u.Host, u.Error)) + "\n")
	}
	b.WriteString("\n" + headStyle.Render("Hosts") + "\n")
	if m.hostsErr != nil {
		b.WriteString(badStyle.Render("  "+m.hostsErr.Error()) + "\n")
	}
	rows := [][]string{{"NAME", "KIND", "REACHABLE", "ENGINE", "ORGS", "ADDRESS"}}
	for _, h := range m.hosts.Hosts {
		n := "-"
		if h.Orgs != nil {
			n = fmt.Sprint(*h.Orgs)
		}
		engine := strings.TrimSpace(h.Engine + " " + h.Docker)
		rows = append(rows, []string{h.Name, h.Kind, yes(h.Reachable, "yes", "no"), engine, n, h.Address})
	}
	b.WriteString(table(rows, -1))
	return b.String()
}

func (m *Model) detailView() (body, keys string) {
	var bar []string
	for i, t := range tabs {
		label := fmt.Sprintf("%d %s", i+1, t)
		if i == m.tab {
			label = tabOn.Render(label)
		}
		bar = append(bar, label)
	}
	t := tabs[m.tab]
	var b strings.Builder
	b.WriteString(strings.Join(bar, "   ") + "\n\n")
	keys = "tab/1-6 switch · esc back · u/d/r up/down/restart · A attach · S shell · ? help · q quit"
	switch {
	case t == tabLogs:
		b.WriteString(m.logsView())
		return b.String(), keys
	case !m.dLoaded:
		b.WriteString(faintStyle.Render("reading " + m.open + "…"))
		return b.String(), keys
	case m.d.Errs[t] != nil:
		b.WriteString(badStyle.Render(ops.Respell(ops.AsError(m.d.Errs[t]).Error())))
		return b.String(), keys
	}
	d := m.d
	switch t {
	case tabInfo:
		state := goodStyle.Render(d.Info.State)
		if d.Info.State != "running" {
			state = badStyle.Render(d.Info.State)
		}
		remote := d.Info.RemoteURL
		if remote == "" {
			remote = "-"
		}
		key := d.Info.GitPublicKey
		if key == "" {
			key = "-"
		}
		for _, kv := range [][2]string{
			{"Container", d.Info.Container}, {"State", state}, {"Remote Control", remote},
			{"Address", d.Info.Address + yes(d.Info.LocalOnly, " (this machine only)", "")},
			{"SSH", fmt.Sprintf("ssh -p %s %s@%s", port(d.Info.SSHPort), d.Info.User, d.Info.SSHHost)},
			{"Browser terminal", "port " + port(d.Info.TTYDPort)},
			{"Git key", key},
			{"Firewall", fmt.Sprintf("%d entries", len(d.Firewall.Entries))}, {"Repos", fmt.Sprintf("%d registered", len(d.Repos.Repos))},
			{"Variables", fmt.Sprintf("%d", len(d.Env.Vars))}, {"Packages", fmt.Sprintf("%d", len(d.Packages.Packages))},
		} {
			fmt.Fprintf(&b, "  %-18s%s\n", kv[0], kv[1])
		}
	case tabFirewall:
		live := "the org is down"
		if d.Firewall.Live != nil {
			live = *d.Firewall.Live
		}
		b.WriteString("  live: " + live + "\n\n")
		rows := [][]string{{"ENTRY"}}
		for _, e := range d.Firewall.Entries {
			rows = append(rows, []string{e})
		}
		b.WriteString(table(rows, clamp(m.sel[t], len(d.Firewall.Entries))))
		keys = "↑↓ select · a allow · x deny · tab switch · esc back · ? help"
	case tabRepos:
		b.WriteString("  policy: " + d.Repos.Policy + "\n\n")
		rows := [][]string{{"DIR", "REPO", "BRANCH", "STATE"}}
		for _, r := range d.Repos.Repos {
			rows = append(rows, []string{r.Dir, yes(r.Local, "(local)", r.Repo), r.Branch, r.State})
		}
		b.WriteString(table(rows, clamp(m.sel[t], len(d.Repos.Repos))))
		if len(d.Repos.Unregistered) > 0 {
			b.WriteString("\n\n  not registered: " + strings.Join(d.Repos.Unregistered, ", "))
		}
		keys = "↑↓ select · a add · x remove · y sync · tab switch · esc back · ? help"
	case tabEnv:
		if len(d.Env.Vars) == 0 {
			b.WriteString(faintStyle.Render("  no custom variables"))
		}
		for _, v := range d.Env.Vars {
			b.WriteString("  " + v.Name + yes(v.Present, "", "  (listed, not set)") + "\n")
		}
		b.WriteString("\n\n" + faintStyle.Render(ops.Respell("  Names only: values are never shown. Set one: berth env "+m.open+" set KEY")))
	case tabBackups:
		rows := [][]string{{"FILE", "SIZE", "ENCRYPTION", "MODIFIED"}}
		for _, k := range d.Backups.Backups {
			rows = append(rows, []string{k.File, size(k.Size), k.Encryption, k.Modified})
		}
		b.WriteString(table(rows, -1))
		if len(d.Backups.Backups) == 0 {
			b.WriteString("\n" + faintStyle.Render("  no backups of this org in "+d.Backups.Dir))
		}
		keys = "b back up now · tab switch · esc back · ? help"
	}
	return b.String(), keys
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

func (m *Model) logsView() string {
	if m.logsErr != nil {
		return badStyle.Render(ops.Respell(ops.AsError(m.logsErr).Error()))
	}
	if len(m.logs) == 0 {
		return faintStyle.Render("no log lines yet")
	}
	room := max(m.h-6, 1)
	lines := m.logs
	if len(lines) > room {
		lines = lines[len(lines)-room:] // the newest: it follows the log
	}
	// A log line may end in a carriage return, or hold tabs: neither may move the cursor here.
	return strings.NewReplacer("\r", "", "\t", "    ").Replace(strings.Join(lines, "\n"))
}

const helpText = `Everywhere
  ?            this help                 q, ctrl+c   quit
  R            read again now            (every page refreshes by itself)
  u / d / r    start, stop, restart the org: each says what stops and asks first
  A / S        attach to its tmux session / open a shell: the terminal is theirs until you leave

The list
  ↑ ↓ (k j)    select an org            enter       open it

An org's page
  tab, 1-6     switch tab               esc         back to the list
  Firewall     a allow entries          x deny the selected entry
  Repos        a add a repo             x remove the selected one     y sync
  Backups      b back up now
  Logs         follows the container's log

Nothing that restarts a container, or removes something, runs without a "y".
With --read-only the dashboard only reads.`

func (m *Model) overlayView() (body, keys string) {
	switch m.over {
	case help:
		return helpText, "any key closes"
	case confirm:
		return boxStyle.Render(m.ask + "\n\n" + headStyle.Render("Run it? y = yes, anything else = no")), "y run it · any other key cancels"
	case input:
		return boxStyle.Render(m.ask + "\n\n> " + m.typed + "█"), "enter confirm · esc cancel"
	case result:
		style := lipgloss.NewStyle()
		if m.failed {
			style = badStyle
		}
		return style.Render(m.text), "any key closes"
	}
	return "", ""
}
