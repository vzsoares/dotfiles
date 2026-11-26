package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/vzsoares/dotfiles/inbox/internal/inbox"
)

func TestTagOfNamesWhoRunsIt(t *testing.T) {
	prod := inbox.Decision{Prod: &inbox.ProdAction{}}
	cases := []struct {
		d    inbox.Decision
		want string
	}{
		{inbox.Decision{Kind: inbox.KindManual}, "YOU RUN THIS"},
		{inbox.Decision{Kind: inbox.KindAgentRuns}, "AGENT RUNS THIS"},
		{prod, "PROD"},
		{inbox.Decision{}, ""},
	}
	for _, c := range cases {
		if got, _ := tagOf(c.d); got != c.want {
			t.Errorf("%+v: tag %q, want %q", c.d, got, c.want)
		}
	}
}

func TestTagStylesAreDistinctFills(t *testing.T) {
	var buf bytes.Buffer
	r := lipgloss.NewRenderer(&buf)
	r.SetColorProfile(termenv.ANSI)
	seen := map[string]string{}
	for _, label := range []string{"YOU RUN THIS", "AGENT RUNS THIS", "PROD"} {
		out := tagStyle(label).Renderer(r).Render(label)
		if !strings.Contains(out, "\x1b[") {
			t.Fatalf("%s: no colour in %q", label, out)
		}
		if other, dup := seen[out]; dup {
			t.Fatalf("%s and %s render alike: %q", label, other, out)
		}
		seen[out] = label
	}
}

func TestPaintTagsKeepsTheRowText(t *testing.T) {
	for _, row := range []string{
		"› PROD · YOU RUN THIS · spec-s1 · #929 Key rotation · 1 open",
		"▸ #929 · Key rotation · AGENT RUNS THIS · 1 open · 2 items",
		"  a plain row with PRODUCT in it",
	} {
		if got := paintTags(row, nil); got != row {
			t.Errorf("text changed: %q", got)
		}
	}
}

func TestWalkPinsTheTagOverEachCase(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"manual", "YOU RUN THIS  Run it yourself, then pick Done and type y."},
		{"agent", "AGENT RUNS THIS  The agent runs this after you answer. Don't run it yourself."},
		{"prod", "PROD  Approve and the agent runs it."},
		{"plain", ""},
	} {
		d := newDrvWith(t, 92, 29, userPanel)
		switch c.name {
		case "manual":
			d.askManual(manualBody)
		case "agent":
			d.askBody()
		case "prod":
			d.askProd("s1")
		default:
			d.ask("7", "s2", "Which way?", "left", "right")
		}
		d.openWalk()
		v := d.text()
		if c.want == "" {
			for _, tag := range []string{"YOU RUN THIS", "AGENT RUNS THIS", "PROD"} {
				if strings.Contains(v, tag) {
					t.Fatalf("%s: unexpected %s:\n%s", c.name, tag, v)
				}
			}
			continue
		}
		if !strings.Contains(v, c.want) {
			t.Fatalf("%s: %q missing:\n%s", c.name, c.want, v)
		}
	}
}

// TestWriteProof dumps each case when ZEN_PROOF_DIR is set: .txt as the pane
// reads, .ansi with the tag colours (cat it in a terminal).
func TestWriteProof(t *testing.T) {
	dir := os.Getenv("ZEN_PROOF_DIR")
	if dir == "" {
		t.Skip("set ZEN_PROOF_DIR to write the proof dumps")
	}
	t.Setenv("ZEN_INBOX_NOW", pinnedNow)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lipgloss.DefaultRenderer().SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.DefaultRenderer().SetColorProfile(termenv.Ascii) })
	dump := func(name string, d *drv) {
		v := d.view()
		plain := ansi.Strip(v)
		for ext, body := range map[string]string{".txt": plain, ".ansi": v} {
			if err := os.WriteFile(filepath.Join(dir, name+ext), []byte(body+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, sz := range [][2]int{{92, 29}, {44, 24}} {
		suffix := fmt.Sprintf("-%dx%d", sz[0], sz[1])
		d := newDrvWith(t, sz[0], sz[1], userPanel)
		d.askManual(manualBody)
		d.openWalk()
		dump("manual-walk"+suffix, d)
		d.keys("a")
		dump("manual-confirm"+suffix, d)

		d = newDrvWith(t, sz[0], sz[1], userPanel)
		d.askBody()
		d.copies()
		d.openWalk()
		dump("agent-runs-walk"+suffix, d)
		d.keys("y", "1")
		dump("agent-runs-copied"+suffix, d)
		d.keys("y")
		d.poll()
		dump("agent-runs-ran-it"+suffix, d)

		d = newDrvWith(t, sz[0], sz[1], userPanel)
		d.askProd("s1")
		d.openWalk()
		dump("prod-walk"+suffix, d)
		d.keys("a")
		dump("prod-confirm"+suffix, d)

		d = newDrvWith(t, sz[0], sz[1], userPanel)
		d.ask("7", "s2", "Which way?", "left", "right")
		d.openWalk()
		dump("plain-walk"+suffix, d)

		d = newDrvWith(t, sz[0], sz[1], userPanel)
		d.askManual(manualBody)
		d.askBody()
		d.askProd("s3")
		d.ask("7", "s2", "Which way?", "left", "right")
		d.poll()
		dump("list"+suffix, d)
		d.keys("enter")
		dump("list-expanded"+suffix, d)
	}
}
