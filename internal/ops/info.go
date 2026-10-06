package ops

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// GetInfo is `info <org>`: every way to connect. A missing SSH_PORT or TTYD_PORT ends it with
// exit 1 and no message, as ccenv does (PARITY.md, legacy quirks).
func GetInfo(ctx context.Context, s System, org string) (Info, error) {
	if err := NeedOrg(s, org); err != nil {
		return Info{}, err
	}
	in := Info{Schema: "berth.info/v1", Org: org, Container: contract.Container(org), State: "stopped", User: "node", SSHHost: contract.Container(org)}
	in.AddressText = s.Address(ctx, org)
	in.Address, _, _ = strings.Cut(in.AddressText, " ")
	in.LocalOnly = in.Address == "127.0.0.1"
	// ccenv: `sp=$(envval "$org" SSH_PORT)` at function level under set -e -o pipefail, so a missing
	// key ends the command with exit 1 and no message.
	var ok bool
	if in.SSHRaw, ok = s.EnvGet(org, "SSH_PORT"); !ok {
		return in, quiet(KindState, 1, "", "%s's org.env has no SSH_PORT", org)
	}
	if in.TTYDRaw, ok = s.EnvGet(org, "TTYD_PORT"); !ok {
		return in, quiet(KindState, 1, "", "%s's org.env has no TTYD_PORT", org)
	}
	in.SSHPort, in.TTYDPort = PortOf(in.SSHRaw), PortOf(in.TTYDRaw)
	if Running(ctx, s, org) {
		in.RemoteURL = RemoteURL(ctx, s, org)
	}
	// The heredoc is expanded in full (a second docker ps, then cat) before anything is printed.
	if Running(ctx, s, org) {
		in.State = "running"
	}
	p := path.Join(s.OrgsDir(), org, "ssh", "id_ed25519.pub")
	if b, err := s.ReadFile(p); err == nil {
		in.GitPublicKey = strings.TrimRight(string(b), "\n") // $(cat f)
	} else {
		in.KeyError = "cat: " + p + ": " + catError(err)
	}
	in.Tunnel = s.Tunnel(ctx, org, in.SSHRaw, in.TTYDRaw)
	return in, nil
}

// catError is the reason cat gives for a file it can't read.
func catError(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "No such file or directory"
	case errors.Is(err, fs.ErrPermission):
		return "Permission denied"
	}
	return err.Error()
}

// GetDefaultOrg is `use` with no arguments.
func GetDefaultOrg(s System) DefaultOrg {
	d := DefaultOrg{Schema: "berth.default-org/v1"}
	if o := s.DefaultOrg(); o != "" {
		d.Org = &o
	}
	return d
}

// GetImage is `image-tag`.
func GetImage(s System) (Image, error) {
	tag, err := s.Image()
	return Image{Schema: "berth.image/v1", Tag: tag}, err
}
