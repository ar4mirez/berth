// Package org reads and writes an org's on-disk state the way legacy ccenv does.
//
// org.env is not parsed as a dotenv file. ccenv reads it with grep/tail/cut and writes it with awk,
// so there is no quoting, no escaping and no comment syntax beyond "doesn't start with KEY=".
// The functions here reproduce those tools byte for byte, and org_test.go checks them against the
// real functions extracted from legacy/ccenv.
package org

import (
	"bytes"
	"strings"
)

// Lookup mirrors ccenv's envval:
//
//	grep -E "^KEY=" org.env | tail -1 | cut -d= -f2-
//
// The last line starting with KEY= wins, and its value is everything after the first '=', raw:
// quotes, spaces, '$', '=' and a CRLF file's trailing '\r' are all kept.
//
// found is false when no line matches, where envval prints nothing. Note that some ccenv call
// sites (`x=$(envval …)` at function level, under set -e -o pipefail) then exit 1 silently; the
// caller decides whether to mirror that.
func Lookup(data []byte, key string) (value string, found bool) {
	prefix := key + "="
	for _, line := range lines(data) {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			value, found = v, true
		}
	}
	return value, found
}

// Set mirrors ccenv's setval (an awk program, then `cat tmp > org.env`):
//
//	$0 ~ "^"k"=" {print k"="v; d=1; next} {print} END{if(!d) print k"="v}
//
// Every line starting with KEY= is replaced by KEY=VALUE (duplicates stay duplicates), and if
// there was none it is appended. Like awk's print, every output line ends in '\n', including
// one that had no newline before. The value is written raw.
func Set(data []byte, key, value string) []byte {
	prefix := key + "="
	var b bytes.Buffer
	done := false
	for _, line := range lines(data) {
		if strings.HasPrefix(line, prefix) {
			line, done = prefix+value, true
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if !done {
		b.WriteString(prefix + value + "\n")
	}
	return b.Bytes()
}

// lines splits data into records the way grep and awk do: on '\n', with a final newline ending
// the last record rather than starting an empty one. '\r' is part of the record.
func lines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	ls := strings.Split(string(data), "\n")
	if ls[len(ls)-1] == "" {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// portValue mirrors next_port's per-file read, `p=$(grep -E "^KEY=" f | cut -d= -f2)`: no
// `tail -1`, and -f2 rather than -f2-. Every matching line contributes the text between the first
// and second '=', joined by newlines, and the command substitution strips trailing newlines.
func portValue(data []byte, key string) string {
	prefix := key + "="
	var vals []string
	for _, line := range lines(data) {
		if v, ok := strings.CutPrefix(line, prefix); ok {
			v, _, _ = strings.Cut(v, "=")
			vals = append(vals, v)
		}
	}
	return strings.TrimRight(strings.Join(vals, "\n"), "\n")
}

// bashInt parses s the way bash's test builtin does for -gt (legal_number): optional spaces or
// tabs, then strtoimax (which also skips leading '\n', '\v', '\f', '\r', takes an optional sign
// and decimal digits), then only spaces or tabs to the end. Anything else, including overflow,
// is "integer expression expected".
func bashInt(s string) (int64, bool) {
	i := 0
	for i < len(s) && strings.IndexByte(" \t\n\v\f\r", s[i]) >= 0 {
		i++
	}
	neg := false
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		neg = s[i] == '-'
		i++
	}
	start := i
	var n int64
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		d := int64(s[i] - '0')
		if n > (1<<63-1-d)/10 {
			return 0, false // ERANGE
		}
		n = n*10 + d
		i++
	}
	if i == start {
		return 0, false
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i != len(s) {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}
