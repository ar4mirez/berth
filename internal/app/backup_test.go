package app

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestHumanMatchesNumfmt: human is `numfmt --to=iec --suffix=B`, checked against the real one.
func TestHumanMatchesNumfmt(t *testing.T) {
	if _, err := exec.LookPath("numfmt"); err != nil {
		t.Skip("no numfmt")
	}
	var ns []int64
	for _, base := range []int64{1, 1 << 10, 1 << 20, 1 << 30, 1 << 40} {
		for _, k := range []int64{0, 1, 9, 10, 99, 100, 512, 1000, 1023, 1024} {
			for _, d := range []int64{-1, 0, 1} {
				if n := base*k + d; n >= 0 {
					ns = append(ns, n)
				}
			}
		}
	}
	ns = append(ns, 10239, 10240, 10752, 5000000, 999999999, 1048575, 3000000000000, 1<<60)
	args := []string{"--to=iec", "--suffix=B"}
	for _, n := range ns {
		args = append(args, strconv.FormatInt(n, 10))
	}
	out, err := exec.Command("numfmt", args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(out))
	for i, n := range ns {
		if got := human(n); got != want[i] {
			t.Errorf("human(%d) = %s, numfmt says %s", n, got, want[i])
		}
	}
}

func TestHeadLines(t *testing.T) {
	for in, want := range map[string]string{"": "", "a": "a", "a\nb\nc\nd\n": "a\nb\nc\n", "a\nb\nc": "a\nb\nc", "a\nb\n": "a\nb\n"} {
		if got := string(headLines([]byte(in), 3)); got != want {
			t.Errorf("headLines(%q) = %q, want %q", in, got, want)
		}
	}
}
