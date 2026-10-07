package ops

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ar4mirez/berth/internal/contract"
)

// Operations the command line has no command of its own for: what an agent or a dashboard asks
// (#61, #59), where a person would read a stream or a directory.

// Logs is the end of an org's container log.
type Logs struct {
	Org string `json:"org"`
	// Lines are the container's last log lines, oldest first (stdout and stderr together).
	Lines []string `json:"lines"`
}

// MaxLogLines is the most TailLogs returns.
const MaxLogLines = 1000

// TailLogs is the last n lines of an org's container log (100 if n isn't positive, at most
// MaxLogLines). Where `logs` follows the log until interrupted, this returns.
func TailLogs(ctx context.Context, s System, org string, n int) (Logs, error) {
	out := Logs{Org: org, Lines: []string{}}
	if err := NeedOrg(s, org); err != nil {
		return out, err
	}
	if err := NeedUp(ctx, s, org); err != nil {
		return out, err
	}
	switch {
	case n <= 0:
		n = 100
	case n > MaxLogLines:
		n = MaxLogLines
	}
	raw, err := s.CaptureRaw(ctx, true, "sh", "-c", `docker logs --tail "$1" "$2" 2>&1`, "sh", strconv.Itoa(n), contract.Container(org))
	out.Lines = append(out.Lines, lines([]byte(raw))...)
	return out, err
}

// Backups is the backups in the backups directory.
type Backups struct {
	Dir     string   `json:"dir"`
	Backups []Backup `json:"backups"`
}

// Backup is one backup file.
type Backup struct {
	File string `json:"file"`
	Org  string `json:"org"`
	// Stamp is when it was taken, as its name has it: YYYYMMDD-HHMMSS in the host's local time.
	Stamp string `json:"stamp"`
	// Modified is when the file was last written (RFC 3339, UTC; "" if it can't be read).
	Modified string `json:"modified"`
	Size     int64  `json:"size"`
	// Encryption is "age", "gpg" or "none".
	Encryption string `json:"encryption"`
}

// backupFile is a backup's name: <org>-<YYYYMMDD>-<HHMMSS>.tar.zst[.gpg|.age].
var backupFile = regexp.MustCompile(`^([a-z0-9][a-z0-9-]*)-([0-9]{8}-[0-9]{6})\.tar\.zst(\.gpg|\.age)?$`)

// ListBackups is the backups in the backups directory, newest first, for org if it is given.
func ListBackups(s System, org string) Backups {
	out := Backups{Dir: s.BackupsDir(), Backups: []Backup{}}
	names, err := s.DirNames(out.Dir)
	if err != nil {
		return out
	}
	for _, n := range names {
		m := backupFile.FindStringSubmatch(n)
		if m == nil || (org != "" && m[1] != org) {
			continue
		}
		b := Backup{File: n, Org: m[1], Stamp: m[2], Encryption: "none"}
		if m[3] != "" {
			b.Encryption = strings.TrimPrefix(m[3], ".")
		}
		if size, mod, ok := s.FileSize(out.Dir + "/" + n); ok {
			b.Size, b.Modified = size, mod.UTC().Format(time.RFC3339)
		}
		out.Backups = append(out.Backups, b)
	}
	sort.Slice(out.Backups, func(i, j int) bool {
		if out.Backups[i].Stamp != out.Backups[j].Stamp {
			return out.Backups[i].Stamp > out.Backups[j].Stamp
		}
		return out.Backups[i].File < out.Backups[j].File
	})
	return out
}
