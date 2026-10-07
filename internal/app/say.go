package app

import (
	"fmt"
	"io"

	"github.com/ar4mirez/berth/internal/ops"
)

// sayf prints one of berth's own messages, with the commands it names spelled as this command
// line has them (ops.Respell, #140). The messages are written with ccenv's spellings, which is
// what the parity suite compares.
func sayf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprint(w, ops.Respell(fmt.Sprintf(format, args...)))
}
