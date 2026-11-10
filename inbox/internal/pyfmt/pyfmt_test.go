package pyfmt

import (
	"encoding/json"
	"slices"
	"testing"
)

// Expected values are what CPython 3.13 prints for the same input.
func TestTextWrapMatchesPython(t *testing.T) {
	cases := []struct {
		text, first, rest string
		width             int
		want              []string
	}{
		{"hello world", "", "", 20, []string{"hello world"}},
		{"  lead kept", "", "", 20, []string{"  lead kept"}},
		{"a\tb", "", "", 20, []string{"a       b"}},
		{"supercalifragilisticexpialidocious word", "a) ", "   ", 20,
			[]string{"a) supercalifragilis", "   ticexpialidocious", "   word"}},
		{"one two three four five six", "", "    ", 20, []string{"one two three four", "    five six"}},
		{"   ", "", "", 20, nil},
		{"", "x", "", 20, nil},
	}
	for _, c := range cases {
		if got := TextWrap(c.text, c.width, c.first, c.rest); !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}

func TestSplitLines(t *testing.T) {
	if got := SplitLines("a\r\nb\n\nc\x1cd\n"); !slices.Equal(got, []string{"a", "b", "", "c", "d"}) {
		t.Fatal(got)
	}
	if SplitLines("") != nil {
		t.Fatal("empty")
	}
}

func TestRepr(t *testing.T) {
	for in, want := range map[string]string{
		"c": "'c'", "it's": `"it's"`, `a'b"c`: `'a\'b"c'`, "x\ny": `'x\ny'`, "é": "'é'", "\x00": `'\x00'`,
		" ": `'\xa0'`,
	} {
		if got := Repr(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}

func TestDumps(t *testing.T) {
	got := Dumps(Object{{Key: "s", Value: "é 😀 \"q\" \\ \t\x7f"}, {Key: "b", Value: true}, {Key: "n", Value: int64(3)},
		{Key: "l", Value: []Object{}}})
	want := "{\"s\": \"\\u00e9 \\ud83d\\ude00 \\\"q\\\" \\\\ \\t\\u007f\", \"b\": true, \"n\": 3, \"l\": []}"
	if got != want {
		t.Fatalf("%q\n%q", got, want)
	}
}

func TestStrAndTruthy(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
		ok   bool
	}{
		{nil, "None", false}, {true, "True", true}, {json.Number("0"), "0", false}, {json.Number("42"), "42", true},
		{json.Number("1.5"), "1.5", true}, {"", "", false}, {[]any{"a", json.Number("1")}, "['a', 1]", true},
	} {
		if Str(c.v) != c.want || Truthy(c.v) != c.ok {
			t.Errorf("%v: %q %v", c.v, Str(c.v), Truthy(c.v))
		}
	}
}
