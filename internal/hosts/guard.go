package hosts

import _ "embed"

// GuardScript is the host guard (#48): guard.sh, run with `bash -c "$GuardScript" guard <cmd>`.
//
//go:embed guard.sh
var GuardScript string

// GuardContainer is the guard's container on a host.
const GuardContainer = "berth-host-guard"
