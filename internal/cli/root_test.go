package cli

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Execute(args, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

func TestNoArgsPrintsHelp(t *testing.T) {
	out, errOut, code := run(t)
	if code != 0 || errOut != "" || !strings.Contains(out, "Usage:") {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, errOut, out)
	}
}

func TestHelpForms(t *testing.T) {
	for _, a := range []string{"help", "-h", "--help"} {
		out, _, code := run(t, a)
		if code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("%s: code=%d stdout=%q", a, code, out)
		}
	}
}

func TestUnknownCommandExits1(t *testing.T) {
	out, errOut, code := run(t, "frobnicate")
	if code != 1 || errOut != "" || !strings.Contains(out, "Usage:") {
		t.Fatalf("code=%d stderr=%q stdout=%q", code, errOut, out)
	}
}

func TestErrorPrefix(t *testing.T) {
	_, errOut, code := run(t, "--no-such-flag")
	if code != 1 || !strings.HasPrefix(errOut, "berth: ") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestVersion(t *testing.T) {
	out, _, code := run(t, "--version")
	if code != 0 || !strings.HasPrefix(out, "berth dev") {
		t.Fatalf("code=%d stdout=%q", code, out)
	}
}

func TestNoVersionShorthand(t *testing.T) {
	_, errOut, code := run(t, "-v")
	if code != 1 || !strings.HasPrefix(errOut, "berth: ") {
		t.Fatalf("-v should be an unknown flag: code=%d stderr=%q", code, errOut)
	}
}
