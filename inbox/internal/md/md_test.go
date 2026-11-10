package md

import (
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func colour() *Renderer {
	lr := lipgloss.NewRenderer(io.Discard)
	lr.SetColorProfile(termenv.ANSI)
	return New(lr)
}

func plain() *Renderer { return New(lipgloss.NewRenderer(io.Discard)) }

var sgrParams = regexp.MustCompile(`\x1b\[([0-9;]*)m`)

// colours is every SGR colour parameter in s; 0 and attributes are skipped.
func colours(t *testing.T, s string) []string {
	t.Helper()
	if strings.Contains(s, "38;5;") || strings.Contains(s, "38;2;") ||
		strings.Contains(s, "48;5;") || strings.Contains(s, "48;2;") {
		t.Fatalf("256 or true colour in %q", s)
	}
	var out []string
	for _, m := range sgrParams.FindAllStringSubmatch(s, -1) {
		for _, p := range strings.Split(m[1], ";") {
			switch {
			case p == "" || len(p) == 1:
			case p >= "30" && p <= "37", p >= "40" && p <= "47", p >= "90" && p <= "97":
				out = append(out, p)
			case len(p) == 3 && p >= "100" && p <= "107":
				out = append(out, p)
			default:
				t.Fatalf("SGR %q outside ANSI 16 in %q", p, s)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func TestInlineCodeHidesBackticksAndColoursOnlyTheCode(t *testing.T) {
	out := strings.Join(colour().Lines("run `go vet` now", 40), "\n")
	if strings.Contains(ansi.Strip(out), "`") {
		t.Fatalf("backticks kept: %q", out)
	}
	if !strings.Contains(out, "\x1b[36mgo vet") {
		t.Fatalf("code not coloured: %q", out)
	}
	if !strings.HasPrefix(out, "run ") {
		t.Fatalf("prose took a colour: %q", out)
	}
	colours(t, out)
}

func TestFencedGoGetsTokenColours(t *testing.T) {
	text := "Try:\n\n```go\nfunc main() {\n\tx := \"hi\" // note\n\treturn 42\n}\n```"
	out := strings.Join(colour().Lines(text, 60), "\n")
	if strings.Contains(ansi.Strip(out), "```") {
		t.Fatalf("fence kept: %q", out)
	}
	if got := colours(t, out); len(got) < 2 {
		t.Fatalf("want token colours, got %v in %q", got, out)
	}
	if !strings.Contains(ansi.Strip(out), "func main() {") {
		t.Fatalf("code text lost: %q", ansi.Strip(out))
	}
}

func TestFenceWithoutKnownLanguageGetsOneColour(t *testing.T) {
	for _, fence := range []string{"```", "```nosuchlang"} {
		out := strings.Join(colour().Lines(fence+"\nplain block here\n```", 60), "\n")
		if !strings.Contains(ansi.Strip(out), "plain block here") {
			t.Fatalf("%s: %q", fence, out)
		}
		if got := colours(t, out); !slices.Equal(got, []string{"36"}) {
			t.Errorf("%s: colours %v in %q", fence, got, out)
		}
	}
}

func TestLongTokensHardWrapToWidth(t *testing.T) {
	token := strings.Repeat("x", 120)
	text := "see " + token + " end\n\n```go\nvar s = \"" + strings.Repeat("y", 110) + "\"\n```"
	for _, r := range []*Renderer{colour(), plain()} {
		for _, w := range []int{20, 38, 90} {
			lines := r.Lines(text, w)
			for _, l := range lines {
				if lipgloss.Width(l) > w {
					t.Errorf("width %d: %d-wide line %q", w, lipgloss.Width(l), l)
				}
				if strings.Contains(l, "\x1b") && !strings.HasSuffix(l, reset) {
					t.Errorf("width %d: line left colour open: %q", w, l)
				}
			}
			joined := ansi.Strip(strings.Join(lines, ""))
			if strings.Count(joined, "x") != 120 || strings.Count(joined, "y") != 110 {
				t.Errorf("width %d lost text: %q", w, joined)
			}
		}
	}
}

func TestAsciiAndNoColorWriteNoEscapes(t *testing.T) {
	text := "a `b` **c**\n\n```go\nx := 1\n```"
	if out := strings.Join(plain().Lines(text, 40), "\n"); strings.Contains(out, "\x1b") {
		t.Fatalf("Ascii wrote escapes: %q", out)
	}
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "")
	tty := lipgloss.NewRenderer(io.Discard, termenv.WithTTY(true))
	if tty.ColorProfile() == termenv.Ascii {
		t.Skip("terminal profile not detected; NO_COLOR check would prove nothing")
	}
	t.Setenv("NO_COLOR", "1")
	r := New(lipgloss.NewRenderer(io.Discard, termenv.WithTTY(true)))
	if out := strings.Join(r.Lines(text, 40), "\n"); strings.Contains(out, "\x1b") {
		t.Fatalf("NO_COLOR wrote escapes: %q", out)
	}
}

func TestBlankEdgesDroppedAndNewlinesKept(t *testing.T) {
	got := plain().Lines("\n\none\ntwo\n\n", 40)
	if !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("%q", got)
	}
}

func TestCacheKeysOnRevFieldAndWidth(t *testing.T) {
	r := colour()
	k := Key{ID: "7.1", Rev: 1, Field: "question", Width: 30}
	a := r.Cached(k, "same `text`")
	b := r.Cached(k, "same `text`")
	if &a[0] != &b[0] || len(r.cache) != 1 {
		t.Fatal("same key and text rendered again")
	}
	k.Rev = 2
	r.Cached(k, "same `text`")
	k.Width = 10
	r.Cached(k, "same `text`")
	if len(r.cache) != 3 {
		t.Fatalf("rev or width change did not render again: %d entries", len(r.cache))
	}
	if got := r.Cached(k, "changed"); ansi.Strip(got[0]) != "changed" {
		t.Fatalf("new text under the same key: %q", got)
	}
}
