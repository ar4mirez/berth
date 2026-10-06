package ops

import (
	"context"
	"fmt"
	"strings"

	"github.com/ar4mirez/berth/internal/contract"
)

// RemoteLogScript is rc_log's pipeline: the last n non-blank lines of the Remote Control log, ANSI
// stripped.
func RemoteLogScript(n int) string {
	return fmt.Sprintf(`sed -e 's/\x1b\][^\x1b]*\x1b\\//g' -e 's/\x1b\[[0-9;]*[A-Za-z]//g' `+contract.RemoteControlLog+` 2>/dev/null | grep -av '^\s*$' | tail -%d`, n)
}

// GetRemoteStatus is `remote <org> status`: Remote Control in a running org. When it is on and
// its log has no capacity line (or can't be read), ccenv's pipeline fails: the status comes back
// with exit 1, after the lines it did print.
func GetRemoteStatus(ctx context.Context, s System, org string) (RemoteStatus, error) {
	st := RemoteStatus{Schema: "berth.remote/v1", Org: org}
	if err := NeedOrg(s, org); err != nil {
		return st, err
	}
	if err := NeedUp(ctx, s, org); err != nil {
		return st, err
	}
	if !RemoteLoggedIn(ctx, s, org) {
		st.State = "login-needed"
		return st, nil
	}
	switch {
	case RemoteRunning(ctx, s, org):
		st.State = "on"
		st.URL = RemoteURL(ctx, s, org)
		// rc_log 200 | grep -a Capacity | tail -1 | sed 's/^ */  /': under pipefail, no Capacity
		// line (grep exits 1) or a failing docker ends the command there, with exit 1.
		log, err := s.CaptureRaw(ctx, false, "docker", "exec", contract.Container(org), "sh", "-c", RemoteLogScript(200))
		for _, l := range lines([]byte(log)) {
			if strings.Contains(l, contract.RemoteControlCapacity) {
				st.Capacity = strings.TrimLeft(l, " ")
			}
		}
		if err != nil || st.Capacity == "" {
			return st, &Exit{Code: 1}
		}
	case RemoteBlocked(ctx, s, org):
		st.State = "blocked-by-org"
	default:
		st.State = "restarting"
	}
	return st, nil
}
