package app

import "testing"

func TestTail(t *testing.T) {
	for in, want := range map[string]string{
		"":                      "",
		"a\n":                   "a\n",
		"a\nb\nc\n":             "a\nb\nc\n",
		"1\n2\n3\n4\n5\n6\n7\n": "3\n4\n5\n6\n7\n",
		"1\n2\n3\n4\n5\n6\n7":   "3\n4\n5\n6\n7",
		"1\n2\n3\n4\n5\n":       "1\n2\n3\n4\n5\n",
		"\n\n\n\n\n\n":          "\n\n\n\n\n",
	} {
		if got := string(tail([]byte(in), 5)); got != want {
			t.Errorf("tail(%q) = %q, want %q", in, got, want)
		}
	}
}
